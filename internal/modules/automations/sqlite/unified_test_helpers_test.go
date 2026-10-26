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

// storageRunView is a test-only read projection. Persistence remains canonical
// in Run.Executions; these fields preserve assertions against the public v2 view.
type storageRunView struct {
	automations.Run

	Steps           []automations.StepAttempt
	BranchDecisions []automations.BranchDecision
	Delays          []automations.DelayExecution
	Notifications   []automations.NotificationExecution
	Functions       []automations.FunctionExecution
}

func projectedStorageRun(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	record automations.Record,
	run automations.Run,
) storageRunView {
	t.Helper()
	entry, err := repository.GetHistoryEntry(context.Background(), record.ID, string(run.ID))
	if err != nil {
		t.Fatalf("history = %v", err)
	}
	retained := runEntry(entry)
	if retained == nil {
		t.Fatalf("history entry = %T, want Run", entry)
	}
	projection, err := automations.ProjectStepExecutions(retained.Snapshot, retained.Executions)
	if err != nil {
		t.Fatalf("project history executions: %v", err)
	}
	return storageRunView{Run: *retained, Steps: projection.Steps, BranchDecisions: projection.BranchDecisions,
		Delays: projection.Delays, Notifications: projection.Notifications, Functions: projection.Functions}
}

func recordStorageStep(
	ctx context.Context,
	repo *automationssqlite.AutomationRepository,
	runID automations.RunID,
	stepID automations.StepID,
	evidence automations.ReachedStepEvidence,
) error {
	return repo.RecordStep(ctx, automations.StepRecord{RunID: runID, StepID: stepID, Evidence: evidence})
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

func recordStorageDelay(
	ctx context.Context,
	repo *automationssqlite.AutomationRepository,
	start automations.DelayStart,
) error {
	return recordStorageStep(ctx, repo, start.RunID, start.StepID, automations.DelayStarted{StartedAt: start.StartedAt})
}

func completeStorageDelay(
	ctx context.Context,
	repo *automationssqlite.AutomationRepository,
	completion automations.DelayCompletion,
) error {
	return completeStorageStep(ctx, repo, completion.RunID, completion.StepID,
		automations.DelayFinished{CompletedAt: completion.CompletedAt, FailureCode: completion.FailureCode})
}

func recordStorageNotification(
	ctx context.Context,
	repo *automationssqlite.AutomationRepository,
	start automations.NotificationStart,
) error {
	return recordStorageStep(ctx, repo, start.RunID, start.StepID,
		automations.NotificationStarted{StartedAt: start.StartedAt, AttemptID: start.AttemptID})
}

func completeStorageNotification(
	ctx context.Context,
	repo *automationssqlite.AutomationRepository,
	c automations.NotificationCompletion,
) error {
	return completeStorageStep(
		ctx,
		repo,
		c.RunID,
		c.StepID,
		automations.NotificationFinished{
			CompletedAt: c.CompletedAt,
			AttemptID:   c.AttemptID,
			Outcome:     c.Outcome,
			ReasonCode:  c.ReasonCode,
		},
	)
}

func recordStorageFunction(
	ctx context.Context,
	repo *automationssqlite.AutomationRepository,
	start automations.FunctionStart,
) error {
	return recordStorageStep(ctx, repo, start.RunID, start.StepID,
		automations.FunctionStarted{StartedAt: start.StartedAt, InvocationID: start.InvocationID})
}

func completeStorageFunction(
	ctx context.Context,
	repo *automationssqlite.AutomationRepository,
	c automations.FunctionCompletion,
) error {
	var completedAt time.Time
	if c.State != nil {
		if at := automations.FunctionTerminalTime(c.State); at != nil {
			completedAt = *at
		}
	}
	return completeStorageStep(ctx, repo, c.RunID, c.StepID,
		automations.FunctionFinished{CompletedAt: completedAt, InvocationID: c.InvocationID, State: c.State})
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
			CompletedAt: at,
			Reservation: reservation,
			Outcome:     automations.SatisfiedStep{VerifiedCommandID: commandID},
		},
	)
}
