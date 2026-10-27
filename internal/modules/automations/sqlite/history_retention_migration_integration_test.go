//go:build integration

package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func openAutomationMigrationDatabase(t *testing.T, version int64) (*sql.DB, *goose.Provider) {
	t.Helper()
	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "historical.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := scheduleMigrationProvider(t, database)
	if _, err = provider.UpTo(context.Background(), version); err != nil {
		t.Fatal(err)
	}
	return database, provider
}

func TestHistoryRetentionMigrationPreservesHistoryEvidenceAndSchema(t *testing.T) {
	t.Parallel()
	database, provider := openAutomationMigrationDatabase(t, 19)
	var err error
	at := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	record := insertHistoricalAutomation(t, database, validDomainDefinition(t))
	for _, status := range []string{"running", "succeeded", "failed", "interrupted"} {
		id := newRunIDString(t)
		var completed, failure any
		if status != "running" {
			completed = migrationTimestamp
		}
		if status == "failed" || status == "interrupted" {
			failure = "core_restarted"
		}
		mustExec(t, database, insertHistoricalHistoryRunSQL, id, string(record.ID), status, failure, completed, "[]")
		if status == "running" {
			seedV19RetentionChildren(t, database, id)
		}
	}
	insertRetainedHistorySkip(t, database, string(record.ID), at)
	mustExec(t, database, `INSERT INTO automation_holds
		(automation_id, revision, trigger_id, last_receive_order, phase) VALUES (?, 1, 'hold', 1, 'idle')`, string(record.ID))
	mustExec(t, database, `INSERT INTO automation_fact_receipts (fact_id, automation_id, outcome_kind, history_id)
		SELECT fact_id, automation_id, kind, id FROM automation_history WHERE kind = 'skip'`)
	tables := snapshotAutomationCascadeRows(t, database)
	tables = append(tables, "automation_fact_receipts")
	//nolint:unqueryvet // Include the non-cascading deduplication receipts in the exact row oracle.
	mustExec(
		t,
		database,
		`CREATE TEMP TABLE expected_automation_fact_receipts AS SELECT * FROM automation_fact_receipts`,
	)
	mustExec(t, database, `CREATE TEMP TABLE expected_schema AS
		SELECT type, name, tbl_name, sql FROM sqlite_master WHERE name NOT LIKE 'goose%'`)
	for _, direction := range []string{"up", "down", "up again"} {
		if direction == "down" {
			_, err = provider.Down(t.Context())
		} else {
			_, err = provider.UpTo(t.Context(), 20)
		}
		if err != nil {
			t.Fatalf("%s: %v", direction, err)
		}
		for _, table := range tables {
			assertMigrationRowsUnchanged(t, database, table)
		}
		want := 1
		if direction == "down" {
			want = 0
		}
		assertHistoryRetentionMigrationSchema(t, database, want)
		assertScheduleMigrationIntegrity(t, database)
	}
}

func seedV19RetentionChildren(t *testing.T, database *sql.DB, runID string) {
	t.Helper()
	mustExec(t, database, `INSERT INTO automation_run_steps (run_id, position, step_id, status)
		VALUES (?, 0, 'light_on', 'not_attempted')`, runID)
	mustExec(t, database, `INSERT INTO automation_run_branch_decisions (run_id, step_id, position, decision_json)
		VALUES (?, 'branch', 0, '{}')`, runID)
	mustExec(t, database, `INSERT INTO automation_run_delays (run_id, step_id, position, status, started_at)
		VALUES (?, 'wait', 0, 'running', ?)`, runID, migrationTimestamp)
	mustExec(t, database, `INSERT INTO automation_run_notifications
		(run_id, step_id, position, attempt_id, status, started_at)
		VALUES (?, 'notify', 0, 'ntf_01980000-0000-7000-8000-000000000001', 'running', ?)`, runID, migrationTimestamp)
	mustExec(t, database, `INSERT INTO automation_run_functions
		(run_id, step_id, position, invocation_id, started_at, status)
		VALUES (?, 'function', 0, 'afi_01980000-0000-7000-8000-000000000001', ?, 'running')`, runID, migrationTimestamp)
}

func assertHistoryRetentionMigrationSchema(t *testing.T, database *sql.DB, wantIndexes int) {
	t.Helper()
	// Ignoring only the added index, both directions must retain the exact schema.
	for _, pair := range [][2]string{{"expected_schema", "sqlite_master"}, {"sqlite_master", "expected_schema"}} {
		var differences int
		if err := database.QueryRow(`SELECT count(*) FROM (
			SELECT type, name, tbl_name, sql FROM ` + pair[0] + ` WHERE name NOT LIKE 'goose%' AND name <> 'automation_history_retention_idx'
			EXCEPT SELECT type, name, tbl_name, sql FROM ` + pair[1] + ` WHERE name NOT LIKE 'goose%' AND name <> 'automation_history_retention_idx')`).
			Scan(&differences); err != nil || differences != 0 {
			t.Fatalf("changed schema objects = %d, %v", differences, err)
		}
	}
	var indexes int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'automation_history_retention_idx'`).
		Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != wantIndexes {
		t.Fatalf("retention indexes = %d, want %d", indexes, wantIndexes)
	}
}
