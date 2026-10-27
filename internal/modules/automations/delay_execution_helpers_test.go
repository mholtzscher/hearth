package automations_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// The hooks surround real migrated repository operations, not timer callbacks.
type delayWriteRepository struct {
	automations.Repository

	start     func(context.Context, automations.StepExecutionRecord) error
	complete  func(context.Context, automations.StepExecutionCompletion) error
	interrupt func(context.Context, automations.RunID, time.Time) error
}

func (repository *delayWriteRepository) InterruptRun(ctx context.Context, id automations.RunID, at time.Time) error {
	if repository.interrupt != nil {
		return repository.interrupt(ctx, id, at)
	}
	return repository.Repository.InterruptRun(ctx, id, at)
}

func (repository *delayWriteRepository) RecordStep(ctx context.Context, record automations.StepExecutionRecord) error {
	_, startIsDelay := record.Evidence.(automations.DelayStarted)
	if !startIsDelay {
		return repository.Repository.RecordStep(ctx, record)
	}
	if repository.start != nil {
		return repository.start(ctx, record)
	}
	return repository.Repository.RecordStep(ctx, record)
}

func (repository *delayWriteRepository) CompleteStep(
	ctx context.Context,
	completion automations.StepExecutionCompletion,
) error {
	_, isDelay := completion.Evidence.(automations.DelayFinished)
	if !isDelay {
		return repository.Repository.CompleteStep(ctx, completion)
	}
	if repository.complete != nil {
		return repository.complete(ctx, completion)
	}
	return repository.Repository.CompleteStep(ctx, completion)
}

func delayStep(id automations.StepID, duration int64) automations.Step {
	return automations.Step{ID: id, Body: automations.DelayStep{DurationMS: duration}}
}

func awaitDelaySignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("delay boundary was not reached")
	}
}

func admitDelayRun(t *testing.T, service *automations.Service, record automations.Record) automations.Run {
	t.Helper()
	run, err := service.StartManualRun(context.Background(), automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func assertDelayInterrupted(t *testing.T, run *automations.Run, code string) {
	t.Helper()
	if run == nil {
		t.Fatal("missing interrupted Run")
	}
	completed, ok := run.State.(automations.CompletedRun)
	interrupted, interruptedOK := completed.Outcome.(automations.InterruptedRun)
	delay, found := firstDelayExecution(*run)
	delayState, delayInterrupted := delay.State.(automations.DelayInterrupted)
	if !ok || !interruptedOK || interrupted.FailureCode != code || !found || !delayInterrupted ||
		delayState.CompletedAt.IsZero() || delayState.FailureCode != code {
		t.Fatalf("expected atomic delay/Run interruption %s, got %#v", code, run)
	}
}

func assertDelayFaultRecovery(
	t *testing.T,
	service *automations.Service,
	automationID automations.AutomationID,
	runID automations.RunID,
	before *automations.Run,
) {
	t.Helper()
	if err := service.InterruptActiveRuns(context.Background(), runtimeTestNow.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	recovered := runEntry(historyEntry(t, service, automationID, string(runID)))
	beforeDelay, beforeFound := firstDelayExecution(*before)
	recoveredDelay, recoveredFound := firstDelayExecution(*recovered)
	if !beforeFound || !recoveredFound {
		t.Fatal("delay reach missing from sparse execution trace")
	}
	if _, stillRunning := beforeDelay.State.(automations.DelayRunning); stillRunning {
		assertDelayInterrupted(t, recovered, automations.FailureCoreRestarted)
	} else if automations.RunStateStatus(recovered.State) != automations.RunInterrupted || runFailure(recovered.State) == nil ||
		*runFailure(recovered.State) != automations.FailureCoreRestarted || !reflect.DeepEqual(recoveredDelay, beforeDelay) {
		t.Fatalf("recovery rewrote completed delay or retained active parent: %#v", recovered)
	}
}

func firstDelayExecution(run automations.Run) (automations.DelayExecutionDetails, bool) {
	for _, execution := range run.Executions {
		if delay, ok := execution.Details.(automations.DelayExecutionDetails); ok {
			return delay, true
		}
	}
	return automations.DelayExecutionDetails{}, false
}

func delayExecution(t *testing.T, run *automations.Run, stepID automations.StepID) automations.DelayExecutionDetails {
	t.Helper()
	execution, ok := executionForStep(*run, stepID)
	if !ok {
		t.Fatalf("missing reached Delay %q: %#v", stepID, run.Executions)
	}
	delay, ok := execution.Details.(automations.DelayExecutionDetails)
	if !ok {
		t.Fatalf("execution %q is not a Delay: %#v", stepID, execution)
	}
	return delay
}

func delaySucceeded(t *testing.T, run *automations.Run, stepID automations.StepID) bool {
	t.Helper()
	delay := delayExecution(t, run, stepID)
	state, succeeded := delay.State.(automations.DelaySucceeded)
	return succeeded && !state.CompletedAt.IsZero()
}

func delayRunning(t *testing.T, run *automations.Run, stepID automations.StepID) bool {
	t.Helper()
	delay := delayExecution(t, run, stepID)
	_, running := delay.State.(automations.DelayRunning)
	return running
}

func delayInterruptedOrRunning(t *testing.T, run *automations.Run, stepID automations.StepID) bool {
	t.Helper()
	delay := delayExecution(t, run, stepID)
	switch state := delay.State.(type) {
	case automations.DelayRunning:
		return true
	case automations.DelayInterrupted:
		return !state.CompletedAt.IsZero() && state.FailureCode != ""
	case automations.DelaySucceeded:
		return false
	default:
		return false
	}
}
