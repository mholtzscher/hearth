package automations //nolint:testpackage // Spec 2 consumes the transaction-local admission helper tested here.

import (
	"fmt"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations/dbsqlc"
)

func TestAutomationScheduledStorageProvenance(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	automation := createTestAutomation(t, repo)
	id, err := NewAutomationRunID()
	if err != nil {
		t.Fatal(err)
	}
	minute := repo.now().Truncate(time.Minute)
	run := AutomationRunRecord{
		ID: id,
		Snapshot: AutomationRunSnapshot{
			AutomationID: automation.ID,
			Revision:     1,
			Definition:   automation.Definition,
			Timezone:     "UTC",
		},
		Source:            AutomationRunSourceScheduled,
		ScheduledAt:       &minute,
		MatchedTriggerIDs: []AutomationTriggerID{"daily", "also-daily"},
		Status:            AutomationRunStatusRunning,
		StartedAt:         repo.now(),
	}
	if err = repo.transaction(
		t.Context(),
		func(q *dbsqlc.Queries) error { return createAutomationRun(t.Context(), q, run, nil) },
	); err != nil {
		t.Fatal(err)
	}
	run.MatchedTriggerIDs[0] = "mutated"
	run.Snapshot.Definition.Steps[0].Parameters[0] = '!'
	stored, err := repo.GetAutomationRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Source != AutomationRunSourceScheduled || stored.ScheduledAt == nil ||
		!stored.ScheduledAt.Equal(minute) ||
		len(stored.MatchedTriggerIDs) != 2 ||
		stored.MatchedTriggerIDs[0] != "daily" ||
		stored.MatchedTriggerIDs[1] != "also-daily" ||
		len(stored.Steps) != 2 ||
		string(stored.Steps[0].Definition.Parameters) != `{"value":9007199254740993}` {
		t.Fatalf("scheduled provenance: %+v", stored)
	}
	for _, statement := range []string{`UPDATE automation_runs SET matched_trigger_ids_json = '[]'`, `UPDATE automation_runs SET scheduled_at = NULL`, `UPDATE automation_runs SET idempotency_key = 'manual-key'`} {
		if _, err = database.ExecContext(t.Context(), statement); err == nil {
			t.Fatalf("accepted inconsistent scheduled provenance: %s", statement)
		}
	}
	conflict := stored
	conflict.ID, err = NewAutomationRunID()
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.transaction(
		t.Context(),
		func(q *dbsqlc.Queries) error { return createAutomationRun(t.Context(), q, conflict, nil) },
	); err == nil {
		t.Fatal("partial unique index allowed second active run")
	}
	var count int
	if err = database.QueryRowContext(t.Context(), `SELECT count(*) FROM automation_runs`).
		Scan(&count); err != nil ||
		count != 1 {
		t.Fatalf("partial overlap: %d %v", count, err)
	}
}

//nolint:gocognit // Exercise both bounded batches and their dependent cascade assertions together.
func TestAutomationPruneBatchAndCascade(t *testing.T) {
	t.Parallel()
	repo, database := testAutomationRepository(t)
	automation := createTestAutomation(t, repo)
	now := repo.now()
	err := repo.transaction(t.Context(), func(q *dbsqlc.Queries) error {
		for index := range 501 {
			id, idErr := NewAutomationRunID()
			if idErr != nil {
				return idErr
			}
			key := fmt.Sprintf("batch-%d", index)
			run := AutomationRunRecord{
				ID: id,
				Snapshot: AutomationRunSnapshot{
					AutomationID: automation.ID,
					Revision:     1,
					Definition:   automation.Definition,
					Timezone:     "UTC",
				},
				Source:            AutomationRunSourceManual,
				MatchedTriggerIDs: []AutomationTriggerID{},
				StartedAt:         now,
				Status:            AutomationRunStatusRunning,
			}
			if createErr := createAutomationRun(t.Context(), q, run, &key); createErr != nil {
				return createErr
			}
			code := AutomationFailureCoreStopping
			if completeErr := completeAutomationRun(
				t.Context(),
				q,
				AutomationRunCompletion{RunID: id, Status: AutomationRunStatusInterrupted, FailureCode: &code},
				now,
			); completeErr != nil {
				return completeErr
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cutoff := now.Add(time.Nanosecond)
	count, err := repo.PruneAutomationHistory(t.Context(), cutoff)
	if err != nil || count != 500 {
		t.Fatalf("first batch: %d %v", count, err)
	}
	var steps int
	if err = database.QueryRowContext(t.Context(), `SELECT count(*) FROM automation_run_steps`).
		Scan(&steps); err != nil ||
		steps != 2 {
		t.Fatalf("step cascade: %d %v", steps, err)
	}
	count, err = repo.PruneAutomationHistory(t.Context(), cutoff)
	if err != nil || count != 1 {
		t.Fatalf("second batch: %d %v", count, err)
	}
	page, err := repo.ListAutomationRuns(t.Context(), AutomationRunListParams{})
	if err != nil || page.Items == nil || len(page.Items) != 0 || page.HasMore {
		t.Fatalf("empty page: %+v %v", page, err)
	}
	if _, err = repo.GetAutomation(t.Context(), automation.ID); err != nil {
		t.Fatal("history pruning deleted definition", err)
	}
}
