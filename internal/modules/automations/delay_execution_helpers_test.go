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

func (repository *delayWriteRepository) RecordDelayStart(ctx context.Context, start automations.DelayStart) error {
	if repository.start != nil {
		return repository.start(ctx, start)
	}
	return repository.Repository.RecordDelayStart(ctx, start)
}

func (repository *delayWriteRepository) CompleteDelay(
	ctx context.Context,
	completion automations.DelayCompletion,
) error {
	if repository.complete != nil {
		return repository.complete(ctx, completion)
	}
	return repository.Repository.CompleteDelay(ctx, completion)
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
	if !ok || !interruptedOK || interrupted.FailureCode != code || len(run.Delays) != 1 ||
		run.Delays[0].Status != automations.DelayInterrupted ||
		run.Delays[0].FailureCode == nil ||
		*run.Delays[0].FailureCode != code {
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
	if before.Delays[0].Status == automations.DelayRunning {
		assertDelayInterrupted(t, recovered, automations.FailureCoreRestarted)
	} else if automations.RunStateStatus(recovered.State) != automations.RunInterrupted || runFailure(recovered.State) == nil ||
		*runFailure(recovered.State) != automations.FailureCoreRestarted || !reflect.DeepEqual(recovered.Delays, before.Delays) {
		t.Fatalf("recovery rewrote completed delay or retained active parent: %#v", recovered)
	}
}
