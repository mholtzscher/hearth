//go:build integration

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// TestCoreErrorExitPublishesPendingFactsAndJoinsRelay protects the error-exit
// teardown: a pending fact a previous process left in the durable outbox is
// published by the next Core process even when that process fails after the
// relay started, and the relay is joined without a cleanup failure. Rows are
// never discarded to make a failed startup look clean.
func TestCoreErrorExitPublishesPendingFactsAndJoinsRelay(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger, recorder := withRecording(slog.LevelInfo)
	server := startLifecycleNATSServer(t)
	subscriber := newDeviceFactSubscriber(t, server.ClientURL())
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	database := dbtest.OpenMigrated(t, databasePath)
	// The row stands for one a previous Core process durably queued but did not
	// publish, exactly what its bounded drain leaves behind for the next process.
	observationID := insertPendingObservationFact(t, database, factsTraceparent, factsTracestate)
	if closeErr := database.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Close() }()

	runErr := Run(ctx, Config{
		HouseholdTimezone: "UTC", HTTPAddr: blocker.Addr().String(),
		NATSURL: server.ClientURL(), SQLitePath: databasePath,
		Agent: requiredAgentConfig(t),
	}, logger)
	if runErr == nil {
		t.Fatal("Run succeeded with a busy HTTP port")
	}
	if stage := ErrorStage(runErr); stage != "http_listen" {
		t.Fatalf("Run error stage = %q, want http_listen", stage)
	}

	// The error-exit relay drain published the durable row rather than losing it,
	// and the relay was joined: no publication goroutine outlived the process.
	record := subscriber.waitForCausation(t, observationID, natswire.DeviceFactFamilyObservation)
	if record.traceparent != factsTraceparent || record.tracestate != factsTracestate {
		t.Fatalf("error-exit fact trace = %q / %q", record.traceparent, record.tracestate)
	}
	waitForDeviceFactOutboxEmpty(ctx, t, databasePath)
	if cleanup := recordsWithEvent(recorder.snapshot(), "process.cleanup_failed"); len(cleanup) != 0 {
		t.Fatalf("error exit emitted process.cleanup_failed: %#v", cleanup)
	}
}

// insertPendingObservationFact writes one pending Observation outbox row
// directly and returns its durable source observation identity.
func insertPendingObservationFact(
	t *testing.T,
	database *sql.DB,
	traceparent string,
	tracestate string,
) string {
	t.Helper()
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	entityID, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, insertErr := database.ExecContext(
		context.Background(),
		`INSERT INTO device_facts_outbox (
			fact_id, family, entity_id, variant, source_id, correlation_id, created_at,
			traceparent, tracestate, value_json, adapter_received_at, source_updated_at, observed_at
		) VALUES (?, 'observation', ?, 'applied', ?, ?, ?, ?, ?, 'true', ?, NULL, ?)`,
		string(factID), string(entityID), string(observationID), string(correlationID), now,
		traceparent, tracestate, now, now,
	); insertErr != nil {
		t.Fatalf("insert pending observation fact: %v", insertErr)
	}
	return string(observationID)
}
