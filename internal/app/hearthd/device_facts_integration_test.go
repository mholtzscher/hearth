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

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
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

// TestCorePublishesDeviceFactsForPreStartupBacklogAndRestart protects the
// backlog and restart contract: reports broker-acknowledged before Core started
// enter durable history and are then published with their originating trace,
// a report published while Core is down is published by the next Core process,
// and the durable outbox settles on each run.
func TestCorePublishesDeviceFactsForPreStartupBacklogAndRestart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	runtimeID, powerEntityID, eventEntityID := seedDeviceFactsRegistration(ctx, t, databasePath)

	server := startLifecycleNATSServer(t)
	validator := mustDeviceFactValidator(t)
	writer, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	js, err := jetstream.New(writer)
	if err != nil {
		t.Fatal(err)
	}
	// Only the inbound streams exist before Core starts, so the reports below are
	// broker-acknowledged backlog rather than anything Core has seen.
	if _, provisionErr := devicesnats.ProvisionObservationResources(ctx, js); provisionErr != nil {
		t.Fatal(provisionErr)
	}
	if _, provisionErr := devicesnats.ProvisionEntityEventResources(ctx, js); provisionErr != nil {
		t.Fatal(provisionErr)
	}
	subscriber := newDeviceFactSubscriber(t, server.ClientURL())
	backlogObservationID := publishBacklogObservation(
		ctx, t, js, validator, runtimeID, powerEntityID, "obs_01890f47-7a6b-7c4d-8e9f-0123456789b1", "true",
	)
	backlogEventID := publishBacklogEntityEvent(
		ctx, t, js, validator, runtimeID, eventEntityID, "evt_01890f47-7a6b-7c4d-8e9f-0123456789b1", "single_press",
	)

	httpAddress, stopCore, runErrors := startDeviceFactsCore(ctx, t, server.ClientURL(), databasePath)
	defer stopCore()
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)
	// Readiness proves the fact stream and relay are live before the backlog is
	// consumed, so a published fact cannot be a readiness artifact.
	waitForCoreReady(ctx, t, httpAddress, runErrors)

	backlogObservationFact := subscriber.waitForCausation(
		t, backlogObservationID, natswire.DeviceFactFamilyObservation,
	)
	backlogEventFact := subscriber.waitForCausation(
		t, backlogEventID, natswire.DeviceFactFamilyEntityEvent,
	)
	for name, fact := range map[string]deviceFactRecord{
		"backlog observation":  backlogObservationFact,
		"backlog entity event": backlogEventFact,
	} {
		if fact.traceparent != factsTraceparent || fact.tracestate != factsTracestate {
			t.Fatalf("%s fact trace = %q / %q, want %q / %q",
				name, fact.traceparent, fact.tracestate, factsTraceparent, factsTracestate)
		}
	}
	waitForDeviceFactOutboxEmpty(ctx, t, databasePath)
	// Both backlog reports entered durable Core history, and their facts were
	// published only after that record committed.
	backlogDatabase, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backlogDatabase.Close() }()
	var recordedObservations, recordedEvents int
	if queryErr := backlogDatabase.QueryRowContext(
		ctx, "SELECT count(*) FROM observations WHERE observation_id = ?", backlogObservationID,
	).Scan(&recordedObservations); queryErr != nil {
		t.Fatal(queryErr)
	}
	if queryErr := backlogDatabase.QueryRowContext(
		ctx, "SELECT count(*) FROM entity_events WHERE event_id = ?", backlogEventID,
	).Scan(&recordedEvents); queryErr != nil {
		t.Fatal(queryErr)
	}
	if recordedObservations != 1 || recordedEvents != 1 {
		t.Fatalf("backlog durable history = %d observations, %d entity events, want 1 and 1",
			recordedObservations, recordedEvents)
	}
	// Inbound evidence, Device Facts, and independent Adapter health streams exist.
	assertDeviceFactStreamSet(ctx, t, js)

	// Restart: a report acknowledged while Core is down is recorded by the next
	// Core process, which re-reads the durable stream from its own ack floor and
	// publishes the fact for the newly recorded evidence.
	stopDeviceFactsCore(t, stopCore, runErrors)
	restartObservationID := publishBacklogObservation(
		ctx, t, js, validator, runtimeID, powerEntityID, "obs_01890f47-7a6b-7c4d-8e9f-0123456789b2", "false",
	)
	restartAddress, stopRestartedCore, restartedErrors := startDeviceFactsCore(
		ctx, t, server.ClientURL(), databasePath,
	)
	waitForCoreHealthz(ctx, t, restartAddress, restartedErrors)
	waitForCoreReady(ctx, t, restartAddress, restartedErrors)
	restartFact := subscriber.waitForCausation(
		t, restartObservationID, natswire.DeviceFactFamilyObservation,
	)
	if restartFact.traceparent != factsTraceparent || restartFact.tracestate != factsTracestate {
		t.Fatalf("restarted fact trace = %q / %q", restartFact.traceparent, restartFact.tracestate)
	}
	waitForDeviceFactOutboxEmpty(ctx, t, databasePath)
	stopDeviceFactsCore(t, stopRestartedCore, restartedErrors)
	// The restarted run provisioned no duplicate stream set.
	assertDeviceFactStreamSet(ctx, t, js)
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
