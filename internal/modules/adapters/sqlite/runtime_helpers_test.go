//go:build integration

package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

const testRuntime = adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
const replacementRuntime = adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ac")

func runtimeFixture(t *testing.T) (*sql.DB, *adaptersqlite.AdapterRepository, time.Time) {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	return database, adaptersqlite.NewAdapterRepository(database), time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
}

func claimWrite(at time.Time) adapters.ClaimRuntimeWrite {
	return adapters.ClaimRuntimeWrite{
		AdapterID:       "bridge",
		RuntimeID:       testRuntime,
		SoftwareName:    "hearth-test",
		SoftwareVersion: "1.0.0",
		ClaimedAt:       at,
		LeaseExpiresAt:  at.Add(15 * time.Second),
	}
}

func heartbeatWrite(at time.Time) adapters.HeartbeatWrite {
	return adapters.HeartbeatWrite{
		AdapterID:        "bridge",
		RuntimeID:        testRuntime,
		ExternalStatus:   adapters.AdapterHealthHealthy,
		SourceObservedAt: at,
		ReceivedAt:       at,
		LeaseExpiresAt:   at.Add(15 * time.Second),
	}
}

func mustExec(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func countRows(t *testing.T, database *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := database.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func pending(t *testing.T, repository *adaptersqlite.AdapterRepository) adapters.PendingHealthUpdate {
	t.Helper()
	updates, err := repository.ListPendingHealthUpdates(context.Background(), 1)
	if err != nil || len(updates) != 1 {
		t.Fatalf("pending update: count=%d error=%v", len(updates), err)
	}
	return updates[0]
}

func deletePending(t *testing.T, repository *adaptersqlite.AdapterRepository) adapters.PendingHealthUpdate {
	t.Helper()
	update := pending(t, repository)
	if err := repository.DeleteHealthUpdate(context.Background(), update.ID); err != nil {
		t.Fatal(err)
	}
	return update
}

func seedMappedEntityReport(t *testing.T, database *sql.DB, at time.Time) {
	t.Helper()
	stamp := at.Format(time.RFC3339Nano)
	const deviceID = "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	const entityID = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	mustExec(
		t,
		database,
		`INSERT INTO devices (id, kind, name, created_at, updated_at) VALUES (?, 'test', 'test', ?, ?)`,
		deviceID,
		stamp,
		stamp,
	)
	mustExec(
		t,
		database,
		`INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at) VALUES (?, ?, 'test', 'test', '{}', ?, ?)`,
		entityID,
		deviceID,
		stamp,
		stamp,
	)
	mustExec(
		t,
		database,
		`INSERT INTO adapter_bindings (adapter_id, binding_key, device_id, created_at, updated_at) VALUES ('bridge', 'binding', ?, ?, ?)`,
		deviceID,
		stamp,
		stamp,
	)
	mustExec(
		t,
		database,
		`INSERT INTO adapter_entity_mappings (adapter_id, binding_key, entity_key, entity_id, external_entity_id, created_at, updated_at) VALUES ('bridge', 'binding', 'entity', ?, 'external', ?, ?)`,
		entityID,
		stamp,
		stamp,
	)
	mustExec(
		t,
		database,
		`INSERT INTO entity_availability_current (entity_id, adapter_id, runtime_id, status, source_observed_at, evidence_at, current_since) VALUES (?, 'bridge', ?, 'available', ?, ?, ?)`,
		entityID,
		string(testRuntime),
		stamp,
		stamp,
		stamp,
	)
}
