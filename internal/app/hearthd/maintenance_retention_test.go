package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// This test protects the single hourly maintenance pass and fails if Entity
// Event retention is not wired into it, if Observation pruning stops running
// beside it, or if the pass sweeps rows inside a window. The injected interval
// keeps the pass testable without waiting an hour.
func TestMaintenancePrunesBothRetentions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if migrateErr := platformdb.Migrate(ctx, database); migrateErr != nil {
		t.Fatal(migrateErr)
	}
	seedMaintenanceRetentionRows(ctx, t, database)
	if count := countRetentionRows(ctx, database, "entity_events"); count != 2 {
		t.Fatalf("seeded entity events = %d, want 2", count)
	}
	if count := countRetentionRows(ctx, database, "observations"); count != 2 {
		t.Fatalf("seeded observations = %d, want 2", count)
	}

	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service := devices.NewService(
		devices.SQLiteStores(devices.NewSQLiteRepository(database, catalog)),
		nil, catalog, devices.Dependencies{},
	)
	runContext, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	workerStopped := make(chan struct{})
	go func() {
		defer close(workerStopped)
		pruneRetainedHistory(
			runContext, service, slog.New(slog.DiscardHandler),
			30*24*time.Hour, 5*time.Millisecond,
		)
	}()
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		return countRetentionRows(ctx, database, "entity_events") == 1 &&
			countRetentionRows(ctx, database, "observations") == 1, nil
	})
	cancelRun()
	select {
	case <-workerStopped:
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance worker did not stop after cancellation")
	}

	// Only the rows inside their window survive, and the worker touched both
	// retentions in the same pass.
	assertRetentionIDs(ctx, t, database, "entity_events", "event_id",
		[]string{"evt_01890f47-7a6b-7c4d-8e9f-0123456789b2"})
	assertRetentionIDs(ctx, t, database, "observations", "observation_id",
		[]string{"obs_01890f47-7a6b-7c4d-8e9f-0123456789a2"})
}

func seedMaintenanceRetentionRows(ctx context.Context, t *testing.T, database *sql.DB) {
	t.Helper()
	now := time.Now().UTC()
	// The stored retention cutoff compares fixed-width UTC strings, so the
	// fixture uses the same encoding as production.
	sortable := func(value time.Time) string {
		return value.Format("2006-01-02T15:04:05.000000000Z")
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO observations (
			observation_id, adapter_id, entity_id, disposition,
			state_value_json, adapter_received_at, observed_at
		) VALUES
			('obs_01890f47-7a6b-7c4d-8e9f-0123456789a1', 'simulator',
				'ent_01890f47-7a6b-7c4d-8e9f-0123456789a1', 'applied', 'true', ?, ?),
			('obs_01890f47-7a6b-7c4d-8e9f-0123456789a2', 'simulator',
				'ent_01890f47-7a6b-7c4d-8e9f-0123456789a1', 'applied', 'false', ?, ?)`,
		sortable(now.Add(-40*24*time.Hour)), sortable(now.Add(-40*24*time.Hour)),
		sortable(now.Add(-time.Hour)), sortable(now.Add(-time.Hour)),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO entity_events (
			event_id, adapter_id, runtime_id, entity_id, correlation_id, name,
			fingerprint, disposition, rejection_code, emitted_at, received_at, recorded_at
		) VALUES
			('evt_01890f47-7a6b-7c4d-8e9f-0123456789b1', 'simulator',
				'run_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'ent_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'cor_01890f47-7a6b-7c4d-8e9f-0123456789c1', 'single_press',
				zeroblob(32), 'accepted', NULL, ?, ?, ?),
			('evt_01890f47-7a6b-7c4d-8e9f-0123456789b2', 'simulator',
				'run_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'ent_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'cor_01890f47-7a6b-7c4d-8e9f-0123456789c2', 'double_press',
				zeroblob(32), 'accepted', NULL, ?, ?, ?)`,
		sortable(now.Add(-40*24*time.Hour)), sortable(now.Add(-40*24*time.Hour)),
		sortable(now.Add(-40*24*time.Hour)),
		sortable(now), sortable(now), sortable(now),
	); err != nil {
		t.Fatal(err)
	}
}

func countRetentionRows(ctx context.Context, database *sql.DB, table string) int {
	var count int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		return -1
	}
	return count
}

func assertRetentionIDs(
	ctx context.Context,
	t *testing.T,
	database *sql.DB,
	table string,
	column string,
	want []string,
) {
	t.Helper()
	rows, err := database.QueryContext(ctx, "SELECT "+column+" FROM "+table+" ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			t.Fatal(scanErr)
		}
		got = append(got, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	if len(got) != len(want) {
		t.Fatalf("%s IDs = %v, want %v", table, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s IDs = %v, want %v", table, got, want)
		}
	}
}
