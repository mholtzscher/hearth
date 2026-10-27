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
) (json.RawMessage, error) {
	switch evidence.(type) {
	case automations.CommandStarted, automations.NotificationStarted, automations.FunctionStarted:
	case automations.BranchDecided, automations.CommandStopped, automations.DelayStarted,
		automations.StateRead, automations.ValueResolutionFailed, automations.ValuesAssigned:
		return nil, nil
	default:
		return nil, nil
	}
	value, found := fixtureSnapshots.Load(runID)
	if !found {
		return nil, fmt.Errorf("fixture snapshot missing for Run %s", runID)
	}
	step, err := automations.StepWithPreparedSnapshot(value.(automations.Definition), stepID)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	switch body := step.Body.(type) {
	case automations.CommandStep:
		if v, ok := body.Parameters.Body.(automations.LiteralValue); ok {
			fields["parameters"] = v.Value
		}
	case automations.NotificationStep:
		if v, ok := body.Title.Body.(automations.LiteralValue); ok {
			fields["title"] = v.Value
		}
		if v, ok := body.Message.Body.(automations.LiteralValue); ok {
			fields["message"] = v.Value
		}
	case automations.FunctionStep:
		if v, ok := body.Arguments.Body.(automations.LiteralValue); ok {
			fields["arguments"] = v.Value
		}
	case automations.ChooseStep,
		automations.DelayStep,
		automations.IfStep,
		automations.ReadStateStep,
		automations.SetVariablesStep:
		// Wrong-kind probes must reach the repository rather than fail in the helper.
		return nil, nil
	default:
		// Wrong-kind probes must reach the repository rather than fail in the helper.
		return nil, nil
	}
	return json.Marshal(fields)
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
	inputs, err := fixtureInputs(runID, stepID, evidence)
	if err != nil {
		return err
	}
	return repo.RecordStep(
		ctx,
		automations.StepExecutionRecord{RunID: runID, StepID: stepID, Evidence: evidence, Inputs: inputs},
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
