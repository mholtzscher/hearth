//go:build integration

package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// A9: Up preserves v10 data through v20, before v21 resets Automations.
// Down removes reached wait evidence only, not definitions, history, or branches.
func TestDelayMigrationPreservesVersionTenData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "v10.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := scheduleMigrationProvider(t, database)
	if _, err = provider.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	_, run := insertHistoricalStorageRun(t, database, branchingStorageDefinition(t), at)
	// These are pre-upgrade fixtures. Current query code requires the new table.
	mustExec(
		t,
		database,
		`UPDATE automation_history SET run_status = 'succeeded', run_completed_at = run_started_at WHERE id = ?`,
		string(run.ID),
	)
	// Frozen v10 document: current execution codecs do not own historical formats.
	const evaluation = `{"evaluated_at":"2026-10-04T12:00:00Z","result":"false","nodes":[{"id":"matched","kind":"trigger","result":"false","matched_trigger_ids":[]}]}`
	const decision = `{"position":0,"step_id":"route","kind":"choose","evaluated_at":"2026-10-04T12:00:00Z","outcome":"default","evaluations":[{"branch_id":"a","evaluation":` + evaluation + `},{"branch_id":"b","evaluation":` + evaluation + `}]}`
	mustExec(
		t,
		database,
		`INSERT INTO automation_run_branch_decisions (run_id, step_id, position, decision_json) VALUES (?, 'route', 0, ?)`,
		string(run.ID),
		decision,
	)
	tables := []string{
		"automations",
		"automation_history",
		"automation_run_steps",
		"automation_run_branch_decisions",
		"automation_holds",
		"automation_fact_receipts",
		"automation_schedule_watermarks",
	}
	for _, table := range tables {
		//nolint:unqueryvet // Full row snapshots guard the migration's preservation contract.
		mustExec(t, database, `CREATE TEMP TABLE expected_`+table+` AS SELECT * FROM `+table)
	}
	mustExec(
		t,
		database,
		`CREATE TEMP TABLE expected_schema AS SELECT type, name, tbl_name, sql FROM sqlite_master WHERE name NOT LIKE 'goose%'`,
	)
	if _, err = provider.UpTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	// A child row exercises Down even when real evidence exists. Definition and
	// snapshot preservation includes new delay JSON, which Down must not rewrite.
	_, delayed := insertHistoricalStorageRun(t, database, delayStorageDefinition(t), at)
	mustExec(t, database, `INSERT INTO automation_run_delays
		(run_id, step_id, position, status, started_at) VALUES (?, 'first', 0, 'running', ?)`,
		string(delayed.ID), at.Format("2006-01-02T15:04:05.000000000Z"))
	for _, table := range tables {
		mustExec(t, database, `DELETE FROM expected_`+table)
		//nolint:unqueryvet // Migration integrity, not query behavior, is under test.
		mustExec(t, database, `INSERT INTO expected_`+table+` SELECT * FROM `+table)
	}
	if _, err = provider.DownTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	var objects int
	if err = database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('automation_run_delays', 'automation_run_delays_one_running_idx')`).
		Scan(&objects); err != nil ||
		objects != 0 {
		t.Fatalf("Down delay objects = %d, %v", objects, err)
	}
	// Migration 17 deliberately retains the enlarged definition/snapshot byte
	// allowance on Down; every other pre-delay schema object must still survive.
	if err = database.QueryRow(`SELECT count(*) FROM (SELECT type, name, tbl_name, sql FROM expected_schema
		WHERE name NOT IN ('automations', 'automation_history')
		EXCEPT SELECT type, name, tbl_name, sql FROM sqlite_master)`).
		Scan(&objects); err != nil ||
		objects != 0 {
		t.Fatalf("Down lost prior schema = %d, %v", objects, err)
	}
	if _, err = provider.UpTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	if err = database.QueryRow(`SELECT count(*) FROM automation_run_delays`).
		Scan(&objects); err != nil ||
		objects != 0 {
		t.Fatalf("re-upgraded delay evidence = %d, %v", objects, err)
	}
}

// The migrated SQLite schema independently guards row coherence, insert-once
// identity, contiguous-position uniqueness, and one active wait per parent.
func TestDelayMigrationConstraints(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"orphan", "negative position", "overflow position", "unknown status", "running completion", "running reason", "completed no completion", "completed reason", "interrupted no completion", "interrupted no reason", "interrupted bad reason", "duplicate Step", "duplicate position", "two running"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			database, _ := openAutomationMigrationDatabase(t, 11)
			_, run := insertHistoricalStorageRun(t, database, delayStorageDefinition(t), time.Now().UTC())
			runID, stepID, position, status := string(run.ID), "first", 0, "running"
			var completed, failure any
			switch name {
			case "orphan":
				runID = newRunIDString(t)
			case "negative position":
				position = -1
			case "overflow position":
				position = 64
			case "unknown status":
				status = "bad"
			case "running completion":
				completed = migrationTimestamp
			case "running reason":
				failure = "core_stopping"
			case "completed no completion":
				status = "completed"
			case "completed reason":
				status, completed, failure = "completed", migrationTimestamp, "core_stopping"
			case "interrupted no completion":
				status, failure = "interrupted", "core_stopping"
			case "interrupted no reason":
				status, completed = "interrupted", migrationTimestamp
			case "interrupted bad reason":
				status, completed, failure = "interrupted", migrationTimestamp, "bad"
			case "duplicate Step", "duplicate position", "two running":
				mustExec(
					t,
					database,
					`INSERT INTO automation_run_delays (run_id, step_id, position, status, started_at) VALUES (?, 'first', 0, 'running', ?)`,
					runID,
					migrationTimestamp,
				)
				if name == "duplicate Step" {
					position, status, completed = 1, "completed", migrationTimestamp
				}
				if name == "duplicate position" {
					stepID, status, completed = "second", "completed", migrationTimestamp
				}
				if name == "two running" {
					stepID, position = "second", 1
				}
			}
			if _, err := database.Exec(
				`INSERT INTO automation_run_delays (run_id, step_id, position, status, started_at, completed_at, failure_code) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				runID,
				stepID,
				position,
				status,
				migrationTimestamp,
				completed,
				failure,
			); err == nil {
				t.Fatal("invalid persisted row accepted")
			}
		})
	}
}
