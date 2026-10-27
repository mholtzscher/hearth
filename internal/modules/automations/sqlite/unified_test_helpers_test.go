//go:build integration

package sqlite_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

//nolint:gochecknoglobals // Per-Run fixtures are concurrent, isolated, and removed at test cleanup.
var fixtureSnapshots sync.Map // RunID -> immutable prepared snapshot, for fixture evidence only.

func initializeStorageRun(t *testing.T, repo *automationssqlite.AutomationRepository, run automations.Run) {
	t.Helper()
	init := automations.RunValueInitialization{
		RecordedAt: run.StartedAt,
		Body:       automations.ValuesInitialized{Values: json.RawMessage(`{}`)},
	}
	if err := repo.RecordRunValueInitialization(t.Context(), run.ID, init); err != nil {
		t.Fatal(err)
	}
	fixtureSnapshots.Store(run.ID, run.Snapshot)
	t.Cleanup(func() { fixtureSnapshots.Delete(run.ID) })
}

func fixtureInputs(
	runID automations.RunID,
	stepID automations.StepID,
	evidence automations.InitialStepEvidence,
) (automations.InitialStepEvidence, error) {
	switch evidence.(type) {
	case automations.CommandStarted, automations.NotificationStarted, automations.FunctionStarted:
	case automations.BranchDecided, automations.CommandStopped, automations.DelayStarted,
		automations.StateRead, automations.ValueResolutionFailed, automations.ValuesAssigned:
		return evidence, nil
	default:
		return evidence, nil
	}
	value, found := fixtureSnapshots.Load(runID)
	if !found {
		return nil, fmt.Errorf("fixture snapshot missing for Run %s", runID)
	}
	step, err := automations.StepWithPreparedSnapshot(value.(automations.Definition), stepID)
	if err != nil {
		return nil, err
	}
	// Wrong-kind probes reach the repository unchanged.
	switch e := evidence.(type) {
	case automations.CommandStarted:
		body, ok := step.Body.(automations.CommandStep)
		if !ok {
			break
		}
		e.Parameters = fixtureLiteral(body.Parameters)
		return e, nil
	case automations.FunctionStarted:
		body, ok := step.Body.(automations.FunctionStep)
		if !ok {
			break
		}
		e.Arguments = fixtureLiteral(body.Arguments)
		return e, nil
	case automations.NotificationStarted:
		body, ok := step.Body.(automations.NotificationStep)
		if !ok {
			break
		}
		e.Message = &notifications.Message{}
		if json.Unmarshal(fixtureLiteral(body.Title), &e.Message.Title) != nil ||
			json.Unmarshal(fixtureLiteral(body.Message), &e.Message.Text) != nil {
			return nil, fmt.Errorf("fixture Notification requires literal text")
		}
		return e, nil
	case automations.BranchDecided, automations.CommandStopped, automations.DelayStarted,
		automations.StateRead, automations.ValueResolutionFailed, automations.ValuesAssigned:
	}
	return evidence, nil
}

func fixtureLiteral(binding automations.ValueBinding) json.RawMessage {
	if literal, ok := binding.Body.(automations.LiteralValue); ok {
		return append(json.RawMessage(nil), literal.Value...)
	}
	return nil
}

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
	evidence, err := fixtureInputs(runID, stepID, evidence)
	if err != nil {
		return err
	}
	return repo.RecordStep(
		ctx,
		automations.StepExecutionRecord{RunID: runID, StepID: stepID, Evidence: evidence},
	)
}

func completeStorageStep(
	ctx context.Context,
	repo *automationssqlite.AutomationRepository,
	runID automations.RunID,
	stepID automations.StepID,
	evidence automations.CompletedStepEvidence,
) error {
	if finished, ok := evidence.(automations.FunctionFinished); ok {
		if _, succeeded := finished.State.(automations.FunctionSucceeded); succeeded && len(finished.Output) == 0 &&
			finished.ValueFailure == nil {
			finished.Output = json.RawMessage(`null`)
			evidence = finished
		}
	}
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
