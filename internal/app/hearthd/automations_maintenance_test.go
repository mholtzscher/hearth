package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// This test protects the app wiring of Automation retention through the shared
// history pruning worker: a pass must prune terminal Runs and Skips older than
// the injected `historyRetention`, keep terminal rows inside the window, never
// select a running Run, and leave matched-Fact receipts for deduplication. It
// fails if Automation pruning is dropped from a pass, uses the wrong window, or
// prunes work that must survive.
func TestHistoryPruneSchedulerPrunesAutomationHistory(t *testing.T) {
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
	seedAutomationHistoryRows(ctx, t, database)
	if count := countRetentionRows(ctx, database, "automation_history"); count != 3 {
		t.Fatalf("seeded automation history = %d, want 3", count)
	}

	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	deviceService := devices.NewService(
		devicessqlite.DeviceStores(devicessqlite.NewDeviceRepository(database, catalog)),
		nil, catalog,
		devices.Dependencies{ObservationRetention: 30 * 24 * time.Hour},
	)
	automationService := automations.NewService(
		automationssqlite.NewAutomationRepository(database, automations.Dependencies{}),
		nil,
		automations.Dependencies{HistoryRetention: 30 * 24 * time.Hour},
	)

	runContext, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	worker := startHistoryPruning(
		runContext, slog.New(slog.DiscardHandler), deviceService, automationService,
	)
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		return countRetentionRows(ctx, database, "automation_history") == 2, nil
	})
	if stopErr := worker.Stop(context.Background()); stopErr != nil {
		t.Fatalf("stopping the history prune worker: %v", stopErr)
	}

	// The expired terminal Run is gone; the fresh terminal Run and the running
	// Run survive.
	assertRetentionIDs(ctx, t, database, "automation_history", "id", []string{
		"arn_01890f47-7a6b-7c4d-8e9f-0123456789a2",
		"arn_01890f47-7a6b-7c4d-8e9f-0123456789a3",
	})
	if receipts := countRetentionRows(ctx, database, "automation_fact_receipts"); receipts != 1 {
		t.Fatalf("matched-Fact receipts after pruning = %d, want 1 retained", receipts)
	}
}

func seedAutomationHistoryRows(ctx context.Context, t *testing.T, database *sql.DB) {
	t.Helper()
	now := time.Now().UTC()
	// The stored retention cutoff compares fixed-width UTC strings, so the
	// fixture uses the automations module's own layout.
	sortable := func(value time.Time) string {
		return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	expired := sortable(now.Add(-40 * 24 * time.Hour))
	// A manual Run needs a definition snapshot and no Fact columns; a running Run
	// has no completion time; only manual Runs are used so no Fact summary is
	// required by the history table's family constraint.
	const snapshot = `{"name":"Maintenance","enabled":true,"triggers":[],"steps":[]}`
	if _, err := database.ExecContext(ctx, `
		INSERT INTO automation_history (
			id, automation_id, automation_name, kind, revision, recorded_at,
			run_snapshot_json, run_source, run_status, run_started_at, run_completed_at,
			run_matched_trigger_ids_json, condition_decision_json
		) VALUES
			('arn_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'aut_01890f47-7a6b-7c4d-8e9f-0123456789b0', 'Maintenance',
				'run', 1, ?, ?, 'manual', 'succeeded', ?, ?, '[]',
				'{"mode":"not_configured","bypass_requested":false}'),
			('arn_01890f47-7a6b-7c4d-8e9f-0123456789a2',
				'aut_01890f47-7a6b-7c4d-8e9f-0123456789b0', 'Maintenance',
				'run', 1, ?, ?, 'manual', 'succeeded', ?, ?, '[]',
				'{"mode":"not_configured","bypass_requested":false}'),
			('arn_01890f47-7a6b-7c4d-8e9f-0123456789a3',
				'aut_01890f47-7a6b-7c4d-8e9f-0123456789b0', 'Maintenance',
				'run', 1, ?, ?, 'manual', 'running', ?, NULL, '[]',
				'{"mode":"not_configured","bypass_requested":false}')`,
		expired, snapshot, expired, expired,
		sortable(now), snapshot, sortable(now), sortable(now),
		expired, snapshot, expired,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO automation_fact_receipts (fact_id, automation_id, outcome_kind, history_id)
		VALUES ('fct_01890f47-7a6b-7c4d-8e9f-0123456789c0',
			'aut_01890f47-7a6b-7c4d-8e9f-0123456789b0', 'run',
			'arn_01890f47-7a6b-7c4d-8e9f-0123456789a1')`); err != nil {
		t.Fatal(err)
	}
}
