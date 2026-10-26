package automations_test

import (
	"context"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func branchTriggerCondition(matched bool) automations.Condition {
	leaf := automations.Condition{
		ID:   "source",
		Body: automations.TriggerCondition{TriggerIDs: []automations.TriggerID{"trigger"}},
	}
	if matched { // Manual Runs have no matches, so negation is true.
		return automations.Condition{ID: "manual", Body: automations.NotCondition{Child: leaf}}
	}
	return leaf
}

func branchIf(
	id automations.StepID,
	condition automations.Condition,
	then, otherwise []automations.Step,
) automations.Step {
	return automations.Step{ID: id, Body: automations.IfStep{Conditions: condition, Then: then, Else: otherwise}}
}

func branchChoose(
	conditionA, conditionB automations.Condition,
	commands []automations.Step,
	fallback bool,
) automations.Step {
	step := automations.Step{ID: "route", Body: automations.ChooseStep{
		Branches: []automations.ChooseBranch{
			{ID: "first", Conditions: conditionA, Steps: commands[:1]},
			{ID: "second", Conditions: conditionB, Steps: commands[1:2]},
		},
	}}
	if fallback {
		chooseBody := step.Body.(automations.ChooseStep)
		chooseBody.Default = commands[2:3]
		step.Body = chooseBody
	}
	return step
}

func startBranchRun(
	t *testing.T,
	service *automations.Service,
	definition automations.Definition,
) automations.Run {
	t.Helper()
	record := createRuntimeAutomation(t, service, definition)
	run, err := service.StartManualRun(
		context.Background(),
		automations.ManualRunInput{AutomationID: record.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)
	entry := historyEntry(t, service, record.ID, string(run.ID))
	if runEntry(entry) == nil {
		t.Fatal("expected retained Run")
	}
	return runEntry(entry).Run
}

// A6/A15: the real decision transaction is intercepted only at its return boundary.
type branchWriteRepository struct {
	automations.Repository

	afterCommit  func()
	onWrite      func(context.Context)
	writeErr     error
	commit       bool
	interruptErr error
	writes       int
}

func (repository *branchWriteRepository) RecordStep(ctx context.Context, record automations.StepRecord) error {
	if _, branch := record.Evidence.(automations.BranchDecided); !branch {
		return repository.Repository.RecordStep(ctx, record)
	}
	repository.writes++
	if repository.onWrite != nil {
		repository.onWrite(ctx)
	}
	if repository.commit {
		if err := repository.Repository.RecordStep(ctx, record); err != nil {
			return err
		}
	}
	if repository.afterCommit != nil {
		repository.afterCommit()
	}
	return repository.writeErr
}

func (repository *branchWriteRepository) CompleteRun(ctx context.Context, completion automations.RunCompletion) error {
	if repository.interruptErr != nil &&
		automations.RunOutcomeStatus(completion.Outcome) == automations.RunInterrupted {
		return repository.interruptErr
	}
	return repository.Repository.CompleteRun(ctx, completion)
}

func (repository *branchWriteRepository) InterruptRun(ctx context.Context, id automations.RunID, at time.Time) error {
	if repository.interruptErr != nil {
		return repository.interruptErr
	}
	return repository.Repository.InterruptRun(ctx, id, at)
}

// malformedRunRepository injects a broken prepared value after real admission
// commits. The durable snapshot and command attempts remain valid and unchanged.
type malformedRunRepository struct {
	automations.Repository

	malform func(*automations.Step)
}

func (repository malformedRunRepository) AdmitManualRun(
	ctx context.Context,
	input automations.ManualRunInput,
	snapshot devices.EntityStateSnapshot,
	at time.Time,
) (automations.ManualAdmissionResult, error) {
	result, err := repository.Repository.AdmitManualRun(ctx, input, snapshot, at)
	if err == nil && runEntry(result) != nil {
		repository.malform(&runEntry(result).Snapshot.Steps[0])
	}
	return result, err
}
