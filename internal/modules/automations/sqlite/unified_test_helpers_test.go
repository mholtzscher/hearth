//go:build integration

package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func retainedStorageRun(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	record automations.Record,
	run automations.Run,
) automations.Run {
	t.Helper()
	entry, err := repository.GetHistoryEntry(context.Background(), record.ID, string(run.ID))
	if err != nil {
		t.Fatalf("history = %v", err)
	}
	retained := runEntry(entry)
	if retained == nil {
		t.Fatalf("history entry = %T, want Run", entry)
	}
	return *retained
}

func storageExecution(run automations.Run, stepID automations.StepID) (automations.StepExecution, bool) {
	for _, execution := range run.Executions {
		if execution.StepID == stepID {
			return execution, true
		}
	}
	return automations.StepExecution{}, false
}

func requireStorageExecution(t *testing.T, run automations.Run, stepID automations.StepID) automations.StepExecution {
	t.Helper()
	execution, ok := storageExecution(run, stepID)
	if !ok {
		t.Fatalf("missing reached execution %q: %#v", stepID, run.Executions)
	}
	return execution
}

func recordStorageStep(
	ctx context.Context,
	repo *automationssqlite.AutomationRepository,
	runID automations.RunID,
	stepID automations.StepID,
	evidence automations.InitialStepEvidence,
) error {
	return repo.RecordStep(ctx, automations.StepExecutionRecord{RunID: runID, StepID: stepID, Evidence: evidence})
}

func completeStorageStep(
	ctx context.Context,
	repo *automationssqlite.AutomationRepository,
	runID automations.RunID,
	stepID automations.StepID,
	evidence automations.CompletedStepEvidence,
) error {
	return repo.CompleteStep(ctx, automations.StepExecutionCompletion{RunID: runID, StepID: stepID, Evidence: evidence})
}

func recordStorageBranch(
	ctx context.Context,
	repo *automationssqlite.AutomationRepository,
	runID automations.RunID,
	decision automations.BranchDecision,
) error {
	return recordStorageStep(ctx, repo, runID, decision.StepID, automations.BranchDecided{Decision: decision})
}

func recordStorageCommand(
	ctx context.Context,
	repo *automationssqlite.AutomationRepository,
	runID automations.RunID,
	stepID automations.StepID,
	reservation automations.CommandReservation,
	at time.Time,
) error {
	return recordStorageStep(
		ctx,
		repo,
		runID,
		stepID,
		automations.CommandStarted{StartedAt: at, Reservation: reservation},
	)
}

func completeStorageCommand(
	ctx context.Context,
	t *testing.T,
	repo *automationssqlite.AutomationRepository,
	runID automations.RunID,
	stepID automations.StepID,
	at time.Time,
) error {
	t.Helper()
	commandID := devices.CommandID(newCommandIDString(t))
	reservation := automations.CommandReservation{
		CommandID:     commandID,
		CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
	}
	if err := recordStorageCommand(ctx, repo, runID, stepID, reservation, at); err != nil {
		return err
	}
	return completeStorageStep(
		ctx,
		repo,
		runID,
		stepID,
		automations.CommandFinished{
			State: automations.CommandSucceeded{CompletedAt: at, Reservation: reservation,
				Outcome: automations.SatisfiedCommand{VerifiedCommandID: commandID}},
		},
	)
}
