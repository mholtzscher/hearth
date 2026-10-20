//go:build integration

package nats //nolint:testpackage // Tests exercise package-private relay, mapping and stream behavior.

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// openDeviceFactOutbox opens one migrated SQLite database and exposes its real
// Device Fact outbox, so a test can prove durability across relay restarts.
func openDeviceFactOutbox(
	t *testing.T,
	change *deviceFactChange,
) (*sql.DB, devices.DeviceFactOutbox) {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := devicessqlite.NewDeviceRepository(database, nil)
	return database, &observingDeviceFactOutbox{DeviceFactOutbox: repository, change: change}
}

func insertPendingObservationFact(t *testing.T, database *sql.DB, fact devices.ObservationFact) {
	t.Helper()
	if err := insertObservationDeviceFact(context.Background(), database, fact); err != nil {
		t.Fatalf("insert pending observation fact: %v", err)
	}
}

// insertObservationDeviceFact writes one pending Observation row directly, so a
// test can prove the durable outbox stays writable while a publication is in
// flight. It returns the raw error so a non-test goroutine can report it instead
// of failing from the wrong goroutine.
func insertObservationDeviceFact(
	ctx context.Context,
	database *sql.DB,
	fact devices.ObservationFact,
) error {
	var sourceUpdatedAt any
	var previousValue any
	if fact.SourceUpdatedAt != nil {
		sourceUpdatedAt = fact.SourceUpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	if fact.PreviousValue != nil {
		previousValue = string(fact.PreviousValue)
	}
	_, err := database.ExecContext(
		ctx,
		`INSERT INTO device_facts_outbox (
			fact_id, family, entity_id, variant, source_id, correlation_id, created_at,
			traceparent, tracestate, value_json, previous_value_json, adapter_received_at, source_updated_at, observed_at
		) VALUES (?, 'observation', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(fact.ID),
		string(fact.EntityID),
		string(fact.Disposition),
		string(fact.ObservationID),
		string(fact.CorrelationID),
		fact.CreatedAt.UTC().Format(time.RFC3339Nano),
		fact.Trace.Traceparent,
		fact.Trace.Tracestate,
		string(fact.Value),
		previousValue,
		fact.AdapterReceivedAt.UTC().Format(time.RFC3339Nano),
		sourceUpdatedAt,
		fact.ObservedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert observation device fact: %w", err)
	}
	return nil
}

func insertPendingEntityEventFact(t *testing.T, database *sql.DB, fact devices.EntityEventFact) {
	t.Helper()
	if _, err := database.ExecContext(
		context.Background(),
		`INSERT INTO device_facts_outbox (
			fact_id, family, entity_id, variant, source_id, correlation_id, created_at,
			traceparent, tracestate, reported_at, received_at, recorded_at
		) VALUES (?, 'entity-event', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(fact.ID),
		string(fact.EntityID),
		string(fact.Name),
		string(fact.EventID),
		string(fact.CorrelationID),
		fact.CreatedAt.UTC().Format(time.RFC3339Nano),
		fact.Trace.Traceparent,
		fact.Trace.Tracestate,
		fact.ReportedAt.UTC().Format(time.RFC3339Nano),
		fact.ReceivedAt.UTC().Format(time.RFC3339Nano),
		fact.RecordedAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert pending entity event fact: %v", err)
	}
}

// corruptOutboxRowTimestamp makes one pending row permanently undecodable by
// replacing its commit time with a value no canonical timestamp parser accepts.
// Only a direct store edit can do this: the schema constrains every other column,
// and a corrupt timestamp is exactly the decode drift the relay must fault on
// instead of retrying forever.
func corruptOutboxRowTimestamp(t *testing.T, database *sql.DB, factID devices.DeviceFactID) {
	t.Helper()
	if _, err := database.ExecContext(
		context.Background(),
		`UPDATE device_facts_outbox SET created_at = 'not-a-timestamp' WHERE fact_id = ?`,
		string(factID),
	); err != nil {
		t.Fatalf("corrupt pending device fact timestamp: %v", err)
	}
}

func countPendingDeviceFacts(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRowContext(
		context.Background(), `SELECT COUNT(*) FROM device_facts_outbox`,
	).Scan(&count); err != nil {
		t.Fatalf("count pending device facts: %v", err)
	}
	return count
}

// pendingFactIDs reads the durable outbox identities in enqueue order, so a test
// can prove exactly which rows a fault left blocked.
func pendingFactIDs(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, err := database.QueryContext(
		context.Background(), `SELECT fact_id FROM device_facts_outbox ORDER BY enqueue_order`,
	)
	if err != nil {
		t.Fatalf("read pending device facts: %v", err)
	}
	defer rows.Close()
	identities := make([]string, 0, 1)
	for rows.Next() {
		var factID string
		if scanErr := rows.Scan(&factID); scanErr != nil {
			t.Fatal(scanErr)
		}
		identities = append(identities, factID)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	return identities
}
