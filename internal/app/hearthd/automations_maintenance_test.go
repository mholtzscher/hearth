package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// TestMaintenancePrunesAutomationHistory protects the app wiring of Automation
// retention: the single hourly pass must prune terminal Runs and Skips older
// than the configured `automation_history_retention`, keep terminal rows inside
// the window, never select a running Run, and leave matched-Fact receipts for
// deduplication. It fails if Automation pruning is dropped from the pass, uses
// the wrong window, or prunes work that must survive.
func TestMaintenancePrunesAutomationHistory(t *testing.T) {
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
		devices.SQLiteStores(devices.NewSQLiteRepository(database, catalog)),
		nil, catalog, devices.Dependencies{},
	)
	automationService := automations.NewService(
		automations.NewSQLiteRepository(database, automations.AutomationDependencies{}),
		nil,
		automations.AutomationDependencies{},
	)

	runContext, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	workerStopped := make(chan struct{})
	go func() {
		defer close(workerStopped)
		pruneRetainedHistory(
			runContext, deviceService, automationService, slog.New(slog.DiscardHandler),
			30*24*time.Hour, 30*24*time.Hour, 5*time.Millisecond,
		)
	}()
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		return countRetentionRows(ctx, database, "automation_history") == 2, nil
	})
	cancelRun()
	select {
	case <-workerStopped:
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance worker did not stop after cancellation")
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
			run_matched_trigger_ids_json
		) VALUES
			('arn_01890f47-7a6b-7c4d-8e9f-0123456789a1',
				'aut_01890f47-7a6b-7c4d-8e9f-0123456789b0', 'Maintenance',
				'run', 1, ?, ?, 'manual', 'succeeded', ?, ?, '[]'),
			('arn_01890f47-7a6b-7c4d-8e9f-0123456789a2',
				'aut_01890f47-7a6b-7c4d-8e9f-0123456789b0', 'Maintenance',
				'run', 1, ?, ?, 'manual', 'succeeded', ?, ?, '[]'),
			('arn_01890f47-7a6b-7c4d-8e9f-0123456789a3',
				'aut_01890f47-7a6b-7c4d-8e9f-0123456789b0', 'Maintenance',
				'run', 1, ?, ?, 'manual', 'running', ?, NULL, '[]')`,
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
