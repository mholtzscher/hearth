//go:build integration

package sqlite_test

import (
	"testing"
	"time"
)

func TestHistoryRetentionPreservesIneligibleRowsAndSurvivingEvidence(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	cutoff := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	expired := seedHistoryRetentionCutoff(t, database, cutoff)
	// Receipts intentionally have no history FK and outlive the pruned evidence.
	mustExec(t, database, `INSERT INTO automation_fact_receipts (fact_id, automation_id, outcome_kind, history_id)
		SELECT fact_id, automation_id, kind, id FROM automation_history WHERE kind = 'skip'`)
	tables := []string{"automation_history", "automation_step_executions", "automation_fact_receipts"}
	for _, table := range tables {
		//nolint:unqueryvet // Whole-row oracle protects every surviving evidence column.
		mustExec(t, database, `CREATE TEMP TABLE expected_`+table+` AS SELECT * FROM `+table)
		column := "run_id"
		if table == "automation_history" {
			column = "id"
		}
		if table != "automation_fact_receipts" {
			for _, id := range expired {
				mustExec(t, database, `DELETE FROM expected_`+table+` WHERE `+column+` = ?`, id)
			}
		}
	}
	count, err := repository.DeleteHistoryBefore(t.Context(), cutoff, 500)
	if err != nil || count != 8 {
		t.Fatalf("pruned %d, %v; want 8", count, err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	if count, err = repository.DeleteHistoryBefore(t.Context(), cutoff, 500); err != nil || count != 0 {
		t.Fatalf("repeat prune = %d, %v", count, err)
	}
	assertScheduleMigrationIntegrity(t, database)
}

func TestHistoryRetentionHonorsFiveHundredEntryBatches(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	at := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	parents := []string{newAutomationIDString(t), newAutomationIDString(t)}
	for i := range 503 {
		insertRetainedHistorySkip(t, database, parents[i%len(parents)], at)
	}
	for _, want := range []int64{500, 3, 0} {
		count, err := repository.DeleteHistoryBefore(t.Context(), at.Add(time.Nanosecond), 500)
		if err != nil || count != want {
			t.Fatalf("batch = %d, %v; want %d", count, err, want)
		}
	}
	var remaining int
	if err := database.QueryRow(`SELECT count(*) FROM automation_history`).
		Scan(&remaining); err != nil ||
		remaining != 0 {
		t.Fatalf("remaining history = %d, %v", remaining, err)
	}
}
