//go:build integration

package db //nolint:testpackage // Migration tests require the package-private embedded migration set.

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestFunctionTimestampMigrationPreservesEvidenceAndConstraints(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := newGooseProvider(t, database)
	if _, err = provider.UpTo(ctx, 18); err != nil {
		t.Fatal(err)
	}
	const runID = "arn_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	const stamp = "2026-10-23T12:00:00.000000000Z"
	if _, err = database.ExecContext(ctx, `INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at,
		run_snapshot_json, run_source, run_status, run_started_at,
		run_matched_trigger_ids_json, condition_decision_json
	) VALUES (?, 'aut_01890f47-7a6b-7c4d-8e9f-0123456789ab', 'Function migration', 'run', 1,
		?, '{}', 'manual', 'running', ?, '[]', '{"mode":"not_configured","bypass_requested":false}')`,
		runID, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(ctx, `INSERT INTO automation_run_functions
		(run_id,step_id,position,invocation_id,started_at,status,completed_at,catalog_revision,invocation_outcome)
		VALUES (?, 'first', 0, 'first-invocation', ?, 'succeeded', ?, 'catalog', 'succeeded')`,
		runID, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(ctx, `INSERT INTO automation_run_functions
		(run_id,step_id,position,invocation_id,started_at,status)
		VALUES (?, 'second', 1, 'second-invocation', ?, 'running')`, runID, stamp); err != nil {
		t.Fatal(err)
	}
	before := functionMigrationEvidence(t, database)
	if _, err = provider.UpTo(ctx, 19); err != nil {
		t.Fatal(err)
	}
	if after := functionMigrationEvidence(t, database); after != before {
		t.Fatalf("migration changed evidence: before=%s after=%s", before, after)
	}
	assertNoForeignKeyViolations(ctx, t, database)
	assertFunctionMigrationConstraints(t, database, runID, stamp)
	const regressed = "2026-10-23T11:00:00.000000000Z"
	if _, err = database.ExecContext(ctx, `UPDATE automation_run_functions SET
		status='interrupted', completed_at=?, reason_code='core_restarted' WHERE step_id='second'`, regressed); err != nil {
		t.Fatalf("backward recovery after upgrade: %v", err)
	}
	backward := functionMigrationEvidence(t, database)
	if _, err = provider.DownTo(ctx, 18); err == nil {
		t.Fatal("downgrade accepted evidence the old chronology constraint cannot represent")
	}
	if after := functionMigrationEvidence(t, database); after != backward {
		t.Fatalf("failed downgrade changed evidence: %s", after)
	}
	// A compatible database can still downgrade without dropping its rows.
	if _, err = database.ExecContext(
		ctx,
		`UPDATE automation_run_functions SET completed_at=? WHERE step_id='second'`,
		stamp,
	); err != nil {
		t.Fatal(err)
	}
	compatible := functionMigrationEvidence(t, database)
	if _, err = provider.DownTo(ctx, 18); err != nil {
		t.Fatal(err)
	}
	if after := functionMigrationEvidence(t, database); after != compatible {
		t.Fatalf("downgrade changed evidence: %s", after)
	}
	if _, err = database.ExecContext(
		ctx,
		`UPDATE automation_run_functions SET completed_at=? WHERE step_id='second'`,
		regressed,
	); err == nil {
		t.Fatal("downgrade did not restore chronology constraint")
	}
	if _, err = provider.UpTo(ctx, 19); err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(ctx, `DELETE FROM automation_history WHERE id=?`, runID); err != nil {
		t.Fatal(err)
	}
	if got := functionMigrationEvidence(t, database); got != "[]" {
		t.Fatalf("foreign-key cascade lost: %s", got)
	}
	assertNoForeignKeyViolations(ctx, t, database)
}

func functionMigrationEvidence(t *testing.T, database *sql.DB) string {
	t.Helper()
	var evidence string
	err := database.QueryRowContext(t.Context(), `SELECT json_group_array(json_array(
		run_id,step_id,position,invocation_id,started_at,status,completed_at,catalog_revision,invocation_outcome,reason_code))
		FROM (SELECT run_id,step_id,position,invocation_id,started_at,status,completed_at,catalog_revision,invocation_outcome,reason_code
		FROM automation_run_functions ORDER BY position)`).Scan(&evidence)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func assertFunctionMigrationConstraints(t *testing.T, database *sql.DB, runID, stamp string) {
	t.Helper()
	for _, statement := range []string{
		`UPDATE automation_run_functions SET position=0 WHERE step_id='second'`,
		`UPDATE automation_run_functions SET step_id='first' WHERE step_id='second'`,
		`UPDATE automation_run_functions SET invocation_id='first-invocation' WHERE step_id='second'`,
		`UPDATE automation_run_functions SET status='running', completed_at=NULL, catalog_revision=NULL, invocation_outcome=NULL WHERE step_id='first'`,
		`UPDATE automation_run_functions SET status='succeeded' WHERE step_id='second'`,
		`UPDATE automation_run_functions SET reason_code='invalid' WHERE step_id='first'`,
		`UPDATE automation_run_functions SET run_id='missing' WHERE step_id='second'`,
	} {
		if _, err := database.ExecContext(t.Context(), statement); err == nil {
			t.Fatalf("migration lost constraint: %s", statement)
		}
	}
	var indexSQL string
	if err := database.QueryRowContext(t.Context(), `SELECT sql FROM sqlite_master
		WHERE type='index' AND name='automation_run_functions_one_running_idx'`).Scan(&indexSQL); err != nil {
		t.Fatalf("running index missing: %v", err)
	}
	if _, err := database.ExecContext(t.Context(), `INSERT INTO automation_run_functions
		(run_id,step_id,position,invocation_id,started_at,status) VALUES (?, 'third', 2, 'third-invocation', ?, 'running')`,
		runID, stamp); err == nil {
		t.Fatal("partial running index no longer excludes a second running Function")
	}
}
