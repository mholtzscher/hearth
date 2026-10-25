//go:build integration

package sqlite_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func TestHistoryRetentionMigrationPreservesHistoryEvidenceAndSchema(t *testing.T) {
	t.Parallel()
	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "v19.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := scheduleMigrationProvider(t, database)
	if _, err = provider.UpTo(t.Context(), 19); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	repository := newAutomationRepository(t, database)
	record, err := repository.CreateAutomation(t.Context(), validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"running", "succeeded", "failed", "interrupted"} {
		id := insertRetainedHistoryRun(t, database, string(record.ID), status, at)
		seedRetainedRunChildren(t, database, id)
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
