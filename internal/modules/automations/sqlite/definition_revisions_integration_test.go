//go:build integration

package sqlite_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

//nolint:gocognit // Ordered persistence journey follows one revision through edits, reopen, and collection.
func TestDefinitionRevisionsShareRunsAndSurviveEditsDeletionAndRestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "retained.db")
	database := dbtest.OpenMigrated(t, path)
	t.Cleanup(func() { _ = database.Close() })
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	repository := scheduleRepo(database, &at)
	definition := validDomainDefinition(t)
	definition.Conditions = conditionLeaf("admission", newEntityID(t), automations.ComparisonEqual, "true")
	record, err := repository.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	var runs []automations.Run
	for range 3 {
		admission, admitErr := repository.AdmitManualRun(t.Context(), automations.ManualRunInput{
			AutomationID: record.ID, BypassConditions: true,
		}, stateSnapshotWith(), at)
		if admitErr != nil {
			t.Fatal(admitErr)
		}
		run := admission.(automations.Run)
		if err = finishStorageCommandRun(t, repository, run, at); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, historyEntry(t, repository, record.ID, string(run.ID)).(automations.Run))
		at = at.Add(time.Minute)
	}
	if count := rowCount(t, database, "automation_definition_revisions"); count != 1 {
		t.Fatalf("three unchanged Runs stored %d artifacts", count)
	}
	var repeatedConfiguration int
	if err = database.QueryRow(`SELECT count(*) FROM automation_history
		WHERE json_type(condition_decision_json, '$.snapshot') IS NOT NULL OR definition_revision <> 1`).Scan(&repeatedConfiguration); err != nil || repeatedConfiguration != 0 {
		t.Fatalf("per-Run configuration = %d, %v", repeatedConfiguration, err)
	}
	definition.Name, definition.Enabled, definition.Conditions = "Edited and disabled", false, nil
	updated, err := repository.ReplaceAutomation(t.Context(), record.ID, 1, definition)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("public revision = %d", updated.Revision)
	}
	// Failed optimistic replacement cannot leak an artifact.
	if _, err = repository.ReplaceAutomation(
		t.Context(),
		record.ID,
		1,
		definition,
	); !errors.Is(
		err,
		automations.ErrRevisionConflict,
	) {
		t.Fatalf("stale replacement = %v", err)
	}
	if count := rowCount(t, database, "automation_definition_revisions"); count != 2 {
		t.Fatalf("replacement artifacts = %d", count)
	}
	admission, err := repository.AdmitManualRun(
		t.Context(),
		automations.ManualRunInput{AutomationID: record.ID},
		stateSnapshotWith(),
		at,
	)
	if err != nil {
		t.Fatal(err)
	}
	active := admission.(automations.Run)
	active = historyEntry(t, repository, record.ID, string(active.ID)).(automations.Run)
	if err = repository.DeleteAutomation(t.Context(), record.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.GetAutomation(t.Context(), record.ID); !errors.Is(err, automations.ErrAutomationNotFound) {
		t.Fatalf("deleted head = %v", err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = platformdb.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	repository = scheduleRepo(database, &at)
	for _, want := range runs {
		got := historyEntry(t, repository, record.ID, string(want.ID)).(automations.Run)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("retained Run changed after edit/deletion/reopen: got %#v want %#v", got, want)
		}
	}
	if got := historyEntry(t, repository, record.ID, string(active.ID)).(automations.Run); !reflect.DeepEqual(
		got,
		active,
	) {
		t.Fatalf("active snapshot changed: %#v", got)
	}
	// Pruning some references must not remove an artifact needed by another Run.
	if deleted, pruneErr := repository.DeleteHistoryBefore(
		t.Context(),
		at.Add(time.Hour),
		1,
	); pruneErr != nil ||
		deleted != 1 {
		t.Fatalf("first prune = %d, %v", deleted, pruneErr)
	}
	if count := rowCount(t, database, "automation_definition_revisions"); count != 2 {
		t.Fatalf("pruned shared revision: %d", count)
	}
	if deleted, pruneErr := repository.DeleteHistoryBefore(
		t.Context(),
		at.Add(time.Hour),
		10,
	); pruneErr != nil ||
		deleted != 2 {
		t.Fatalf("terminal prune = %d, %v", deleted, pruneErr)
	}
	if count := rowCount(t, database, "automation_definition_revisions"); count != 1 {
		t.Fatalf("unreferenced revision not collected: %d", count)
	}
	if err = repository.InterruptActiveRuns(t.Context(), at, "core_restarted"); err != nil {
		t.Fatal(err)
	}
	if deleted, pruneErr := repository.DeleteHistoryBefore(
		t.Context(),
		at.Add(time.Hour),
		10,
	); pruneErr != nil ||
		deleted != 1 {
		t.Fatalf("recovered prune = %d, %v", deleted, pruneErr)
	}
	if count := rowCount(t, database, "automation_definition_revisions"); count != 0 {
		t.Fatalf("deleted head's artifact leaked: %d", count)
	}
}

func TestDefinitionRevisionsKeepCurrentHeadAndDoNotPinSkipOnlyRevisions(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	definition := validDomainDefinition(t)
	definition.Conditions = conditionLeaf("admission", newEntityID(t), automations.ComparisonEqual, "true")
	record, err := repository.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	admission, err := repository.AdmitManualRun(
		t.Context(),
		automations.ManualRunInput{AutomationID: record.ID},
		stateSnapshotWith(
			absentEntityEntry(definition.Conditions.Body.(automations.EntityStateCondition).EntityID),
		),
		at,
	)
	if err != nil {
		t.Fatal(err)
	}
	skip := admission.(automations.Skip)
	if skip.Reason != automations.SkipConditionsUnknown {
		t.Fatalf("Skip = %#v", skip)
	}
	if _, err = repository.DeleteHistoryBefore(t.Context(), at.Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	if count := rowCount(t, database, "automation_definition_revisions"); count != 1 {
		t.Fatalf("current head collected: %d", count)
	}
	// Retain another inline Skip, then remove the head. Its partial configuration
	// survives even though the full artifact becomes collectible.
	admission, err = repository.AdmitManualRun(
		t.Context(),
		automations.ManualRunInput{AutomationID: record.ID},
		stateSnapshotWith(
			absentEntityEntry(definition.Conditions.Body.(automations.EntityStateCondition).EntityID),
		),
		at,
	)
	if err != nil {
		t.Fatal(err)
	}
	skip = admission.(automations.Skip)
	if err = repository.DeleteAutomation(t.Context(), record.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.DeleteHistoryBefore(t.Context(), at.Add(-time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	if count := rowCount(t, database, "automation_definition_revisions"); count != 0 {
		t.Fatalf("Skip-only revision pinned %d artifacts", count)
	}
	got := historyEntry(t, repository, record.ID, string(skip.ID)).(automations.Skip)
	if !reflect.DeepEqual(got, skip) {
		t.Fatalf("inline Skip changed: %#v", got)
	}
}

func TestDefinitionRevisionsEnforceImmutableReferencedArtifacts(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	record, run := admitStorageRun(t, repository, validDomainDefinition(t), time.Now().UTC())
	for _, statement := range []string{
		`UPDATE automation_definition_revisions SET definition_json = '{}'`,
		`DELETE FROM automation_definition_revisions`,
		`UPDATE automation_history SET definition_revision = NULL`,
		`UPDATE automation_history SET definition_revision = 2`,
	} {
		if _, err := database.ExecContext(t.Context(), statement); err == nil {
			t.Fatalf("accepted %s", statement)
		}
	}
	// A missing revision in a damaged database must be a corruption error, never
	// a synthesized snapshot or a fallback to the current head.
	mustExec(t, database, `PRAGMA foreign_keys = OFF`)
	mustExec(t, database, `DELETE FROM automation_definition_revisions`)
	mustExec(t, database, `PRAGMA foreign_keys = ON`)
	if _, err := repository.GetHistoryEntry(
		t.Context(),
		record.ID,
		string(run.ID),
	); !isStoredCorruption(err) {
		t.Fatalf("missing revision read = %v", err)
	}
}
