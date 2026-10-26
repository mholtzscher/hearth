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

	start     func(context.Context, automations.DelayStart) error
	complete  func(context.Context, automations.DelayCompletion) error
	interrupt func(context.Context, automations.RunID, time.Time) error
}

func (repository *delayWriteRepository) InterruptRun(ctx context.Context, id automations.RunID, at time.Time) error {
	if repository.interrupt != nil {
		return repository.interrupt(ctx, id, at)
	}
	return repository.Repository.InterruptRun(ctx, id, at)
}

func (repository *delayWriteRepository) RecordStep(ctx context.Context, record automations.StepRecord) error {
	startEvidence, startIsDelay := record.Evidence.(automations.DelayStarted)
	if !startIsDelay {
		return repository.Repository.RecordStep(ctx, record)
	}
	start := automations.DelayStart{RunID: record.RunID, StepID: record.StepID, StartedAt: startEvidence.StartedAt}
	if repository.start != nil {
		return repository.start(ctx, start)
	}
	return repository.Repository.RecordStep(ctx, record)
}

func (repository *delayWriteRepository) CompleteStep(
	ctx context.Context,
	completion automations.StepExecutionCompletion,
) error {
	evidence, isDelay := completion.Evidence.(automations.DelayFinished)
	if !isDelay {
		return repository.Repository.CompleteStep(ctx, completion)
	}
	status := automations.DelayCompleted
	if evidence.FailureCode != nil {
		status = automations.DelayInterrupted
	}
	legacy := automations.DelayCompletion{
		RunID:       completion.RunID,
		StepID:      completion.StepID,
		Status:      status,
		CompletedAt: evidence.CompletedAt,
		FailureCode: evidence.FailureCode,
	}
	if repository.complete != nil {
		return repository.complete(ctx, legacy)
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
	delays := projectRunTrace(t, *run).Delays
	if !ok || !interruptedOK || interrupted.FailureCode != code || len(delays) != 1 ||
		delays[0].Status != automations.DelayInterrupted ||
		delays[0].FailureCode == nil ||
		*delays[0].FailureCode != code {
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
	beforeDelays := projectRunTrace(t, *before).Delays
	recoveredDelays := projectRunTrace(t, recovered.Run).Delays
	if beforeDelays[0].Status == automations.DelayRunning {
		assertDelayInterrupted(t, &recovered.Run, automations.FailureCoreRestarted)
	} else if automations.RunStateStatus(recovered.State) != automations.RunInterrupted || runFailure(recovered.State) == nil ||
		*runFailure(recovered.State) != automations.FailureCoreRestarted || !reflect.DeepEqual(recoveredDelays, beforeDelays) {
		t.Fatalf("recovery rewrote completed delay or retained active parent: %#v", recovered)
	}
}
