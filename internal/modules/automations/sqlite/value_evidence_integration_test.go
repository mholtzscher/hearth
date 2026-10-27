//go:build integration

package sqlite_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestValueInitializationIsInsertOnceAndRequiredBeforeSteps(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repo, validDomainDefinition(t), at)
	ctx := context.Background()
	if err := repo.RecordStep(
		ctx,
		automations.StepExecutionRecord{RunID: run.ID, StepID: "light_on", Evidence: automations.ValueResolutionFailed{
			Kind:    automations.StepKindCommand,
			At:      at,
			Failure: automations.ValueFailure{Path: "/steps/0/parameters", FailureCode: "expression_limit"},
		}},
	); err == nil {
		t.Fatal("Step accepted before initialization")
	}
	init := automations.RunValueInitialization{
		RecordedAt: at,
		Body:       automations.ValuesInitialized{Values: json.RawMessage(`{}`)},
	}
	if err := repo.RecordRunValueInitialization(ctx, run.ID, init); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordRunValueInitialization(ctx, run.ID, init); err == nil {
		t.Fatal("duplicate initialization accepted")
	}
	retained := retainedStorageRun(t, repo, record, run)
	if retained.DataEvidence != automations.RunDataRecorded || retained.ValueInitialization == nil {
		t.Fatal("initialization not retained")
	}
}

func TestValueInitializationFailureAtomicRollback(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repo, validDomainDefinition(t), at)
	ctx := context.Background()
	if _, err := db.Exec(
		`CREATE TRIGGER reject_value_failure BEFORE UPDATE OF run_status ON automation_history BEGIN SELECT RAISE(ABORT, 'injected'); END`,
	); err != nil {
		t.Fatal(err)
	}
	init := automations.RunValueInitialization{
		RecordedAt: at,
		Body: automations.InitializationFailed{
			Failure: automations.ValueFailure{Path: "/variables", FailureCode: "run_data_limit"},
		},
	}
	if err := repo.RecordRunValueInitialization(ctx, run.ID, init); err == nil {
		t.Fatal("injected failure accepted")
	}
	retained := retainedStorageRun(t, repo, record, run)
	if retained.ValueInitialization != nil || automations.RunStateStatus(retained.State) != automations.RunRunning {
		t.Fatal("partial initialization committed")
	}
	if _, err := db.Exec(`DROP TRIGGER reject_value_failure`); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordRunValueInitialization(ctx, run.ID, init); err != nil {
		t.Fatal(err)
	}
	retained = retainedStorageRun(t, repo, record, run)
	if retained.ValueInitialization == nil || automations.RunStateStatus(retained.State) != automations.RunFailed {
		t.Fatal("failure was not committed atomically")
	}
}
