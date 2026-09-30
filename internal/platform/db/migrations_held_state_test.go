package db //nolint:testpackage // Migration tests require the package-private embedded migration set.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestHeldStateMigrationDownPreservesLegacySchemaAndRunSteps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := newGooseProvider(t, database)
	if _, err = provider.UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}

	const runID = "arn_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	const automationID = "aut_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	const stamp = "2026-09-01T00:00:00.000000000Z"
	if _, err = database.ExecContext(ctx, `INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at,
		run_snapshot_json, run_source, run_status, run_started_at,
		run_matched_trigger_ids_json, condition_decision_json
	) VALUES (?, ?, 'Legacy run', 'run', 1, ?, '{}', 'manual', 'running', ?, '[]',
		'{"mode":"not_configured","bypass_requested":false}')`, runID, automationID, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(ctx, `INSERT INTO automation_run_steps (
		run_id, position, step_id, status
	) VALUES (?, 0, 'legacy-step', 'not_attempted')`, runID); err != nil {
		t.Fatal(err)
	}

	if _, err = provider.UpTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	assertNoForeignKeyViolations(ctx, t, database)
	if _, err = provider.DownTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	assertNoForeignKeyViolations(ctx, t, database)

	var stepCount int
	if err = database.QueryRowContext(ctx,
		`SELECT count(*) FROM automation_run_steps WHERE run_id = ?`, runID,
	).Scan(&stepCount); err != nil {
		t.Fatal(err)
	}
	if stepCount != 1 {
		t.Fatalf("run steps after held-state migration down = %d, want 1", stepCount)
	}
	assertColumnAbsent(ctx, t, database, "automation_history", "hold_trigger_id")
	assertColumnPresent(ctx, t, database, "automation_history", "fact_previous_value_json")

	// The legacy Run provenance CHECK must continue rejecting newly written
	// held-state outcomes after downgrade.
	if _, err = database.ExecContext(ctx, `INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at,
		run_snapshot_json, run_source, run_status, run_started_at,
		run_matched_trigger_ids_json, condition_decision_json
	) VALUES ('arn_01890f47-7a6b-7c4d-8e9f-1123456789ab', ?, 'Invalid run', 'run', 1, ?,
		'{}', 'held_state', 'running', ?, '["held-trigger"]',
		'{"mode":"not_configured","bypass_requested":false}')`, automationID, stamp, stamp); err == nil {
		t.Fatal("downgraded history accepted held_state Run provenance")
	}
}

func assertNoForeignKeyViolations(ctx context.Context, t *testing.T, database *sql.DB) {
	t.Helper()
	rows, err := database.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowID, foreignKeyID sql.NullInt64
		if err = rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("foreign-key violation after migration: table=%s parent=%s rowid=%v fk=%v",
			table, parent, rowID, foreignKeyID)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
}
