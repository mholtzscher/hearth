package db //nolint:testpackage // Migration tests require the package-private embedded migration set.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// Migration 00006 must add nullable predecessor columns without inferring
// evidence for existing history/outbox rows, accept real JSON null, and remove
// both columns on downgrade. This fails if the up migration rewrites old rows,
// the down migration leaves a column behind, or the nullable JSON constraint
// rejects JSON null.
func TestObservationPredecessorMigrationPreservesLegacyRowsAcrossUpDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := newGooseProvider(t, database)
	if _, err = provider.UpTo(ctx, 5); err != nil {
		t.Fatal(err)
	}

	const oldRunID = "arn_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	const automationID = "aut_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	const stamp = "2026-09-01T00:00:00.000000000Z"
	if _, err = database.ExecContext(ctx, `INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at,
		run_snapshot_json, run_source, run_status, run_started_at,
		run_matched_trigger_ids_json, condition_decision_json
	) VALUES (?, ?, 'Old manual run', 'run', 1, ?, '{}', 'manual', 'running', ?, '[]',
		'{"mode":"not_configured","bypass_requested":false}')`,
		oldRunID, automationID, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	assertColumnPresent(ctx, t, database, "automation_history", "fact_previous_value_json")
	assertColumnPresent(ctx, t, database, "device_facts_outbox", "previous_value_json")
	var previous sql.NullString
	if err = database.QueryRowContext(ctx,
		`SELECT fact_previous_value_json FROM automation_history WHERE id = ?`, oldRunID,
	).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	if previous.Valid {
		t.Fatalf("legacy history predecessor = %q, want SQL NULL", previous.String)
	}
	if _, err = database.ExecContext(ctx,
		`UPDATE automation_history SET fact_previous_value_json = 'null' WHERE id = ?`, oldRunID,
	); err != nil {
		t.Fatalf("store JSON-null predecessor: %v", err)
	}
	if err = database.QueryRowContext(ctx,
		`SELECT fact_previous_value_json FROM automation_history WHERE id = ?`, oldRunID,
	).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	if !previous.Valid || previous.String != "null" {
		t.Fatalf("JSON-null predecessor = %#v, want valid text null", previous)
	}

	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	assertColumnAbsent(ctx, t, database, "automation_history", "fact_previous_value_json")
	assertColumnAbsent(ctx, t, database, "device_facts_outbox", "previous_value_json")
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = database.QueryRowContext(ctx,
		`SELECT count(*) FROM automation_history WHERE id = ?`, oldRunID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("legacy history rows after down/up = %d, want 1", count)
	}
	if err = database.QueryRowContext(ctx,
		`SELECT fact_previous_value_json FROM automation_history WHERE id = ?`, oldRunID,
	).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	if previous.Valid {
		t.Fatalf("legacy history acquired inferred predecessor after re-up: %q", previous.String)
	}
}

func assertColumnPresent(ctx context.Context, t *testing.T, database *sql.DB, table, column string) {
	t.Helper()
	if !hasSQLiteColumn(ctx, t, database, table, column) {
		t.Fatalf("%s.%s is missing", table, column)
	}
}

func assertColumnAbsent(ctx context.Context, t *testing.T, database *sql.DB, table, column string) {
	t.Helper()
	if hasSQLiteColumn(ctx, t, database, table, column) {
		t.Fatalf("%s.%s remains after migration down", table, column)
	}
}

func hasSQLiteColumn(ctx context.Context, t *testing.T, database *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := database.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return true
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}
