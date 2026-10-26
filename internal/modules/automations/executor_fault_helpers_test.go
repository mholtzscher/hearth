package automations_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Faults surround real SQLite commits. A committed write can still report an
// error, so interruption must preserve its terminal evidence without replay.
type executorFaultRepository struct {
	automations.Repository

	target         automations.AutomationID
	runID          atomic.Value
	boundary       string
	commit         bool
	cleanupFails   bool
	cleanupCalls   atomic.Int32
	cleanupReached chan struct{}
}

var errExecutorWrite = errors.New("sensitive injected database error")

func (repo *executorFaultRepository) AdmitManualRun(
	ctx context.Context, input automations.ManualRunInput, snapshot devices.EntityStateSnapshot, at time.Time,
) (automations.ManualAdmissionResult, error) {
	if input.AutomationID == repo.target && repo.boundary == "admission" {
		return nil, errExecutorWrite
	}
	result, err := repo.Repository.AdmitManualRun(ctx, input, snapshot, at)
	if err == nil && input.AutomationID == repo.target {
		repo.runID.Store(runEntry(result).ID)
	}
	return result, err
}

func (repo *executorFaultRepository) inject(id automations.RunID, boundary string, write func() error) error {
	if id != repo.runID.Load().(automations.RunID) || boundary != repo.boundary {
		return write()
	}
	if repo.commit {
		if err := write(); err != nil {
			return err
		}
	}
	return errExecutorWrite
}

func (repo *executorFaultRepository) RecordStep(ctx context.Context, record automations.StepRecord) error {
	boundary := ""
	switch record.Evidence.(type) {
	case automations.CommandStarted, automations.CommandStoppedBeforeReservation:
		boundary = "start"
	case automations.DelayStarted:
		boundary = "delay_start"
	case automations.NotificationStarted:
		boundary = "notification_start"
	case automations.FunctionStarted:
		boundary = "function_start"
	case automations.BranchDecided:
		boundary = "branch"
	}
	if boundary == "" {
		return repo.Repository.RecordStep(ctx, record)
	}
	return repo.inject(record.RunID, boundary, func() error { return repo.Repository.RecordStep(ctx, record) })
}

func (repo *executorFaultRepository) CompleteStep(
	ctx context.Context,
	completion automations.StepExecutionCompletion,
) error {
	boundary := ""
	switch completion.Evidence.(type) {
	case automations.CommandFinished:
		boundary = "step"
	case automations.DelayFinished:
		boundary = "delay_complete"
	case automations.NotificationFinished:
		boundary = "notification_complete"
	case automations.FunctionFinished:
		boundary = "function_complete"
	}
	if boundary == "" {
		return repo.Repository.CompleteStep(ctx, completion)
	}
	return repo.inject(
		completion.RunID,
		boundary,
		func() error { return repo.Repository.CompleteStep(ctx, completion) },
	)
}

func (repo *executorFaultRepository) CompleteRun(ctx context.Context, completion automations.RunCompletion) error {
	return repo.inject(completion.RunID, "run", func() error { return repo.Repository.CompleteRun(ctx, completion) })
}

func (repo *executorFaultRepository) InterruptRun(ctx context.Context, id automations.RunID, at time.Time) error {
	defer close(repo.cleanupReached)
	repo.cleanupCalls.Add(1)
	if repo.cleanupFails {
		return errExecutorWrite
	}
	return repo.Repository.InterruptRun(ctx, id, at)
}

type executorFaultCase struct {
	name, boundary       string
	commit, cleanupFails bool
	commands             int
}

func blockFaultSiblingCommand(
	t *testing.T,
	scripted *scriptedDevices,
	entity devices.EntityID,
	entered, release chan struct{},
) {
	t.Helper()
	scripted.execute = func(ctx context.Context, input devices.CommandInput) (devices.CommandResult, error) {
		if input.EntityID == entity {
			close(entered)
			<-release
			if ctx.Err() != nil {
				t.Error("Run fault canceled sibling Command")
			}
		}
		scripted.recordCommand(terminalCommand(t, input, devices.CommandStatusSatisfied, nil))
		return devices.CommandResult{CommandID: input.ID, Outcome: devices.OutcomeDispatched}, nil
	}
}

func awaitExecutorFault(t *testing.T, repo *executorFaultRepository, err error) {
	t.Helper()
	if repo.boundary == "admission" {
		if !errors.Is(err, errExecutorWrite) {
			t.Fatalf("admission error = %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	awaitDelaySignal(t, repo.cleanupReached)
}

func assertExecutorFaultCleanup(
	t *testing.T,
	repo *executorFaultRepository,
	service *automations.Service,
	target automations.Record,
	run automations.Run,
	writer *lockedAutomationLogWriter,
) {
	t.Helper()
	if repo.boundary == "admission" {
		if len(listHistory(t, service, target.ID)) != 0 || repo.cleanupCalls.Load() != 0 {
			t.Fatal("failed admission created history or attempted Run cleanup")
		}
		return
	}
	assertExecutorFaultEvidence(t, service, target, run, repo.boundary, repo.commit, repo.cleanupFails)
	if repo.cleanupCalls.Load() != 1 || len(automationLogEvents(writer.records(t), "automation.executor_fault")) != 1 {
		t.Fatal("fault cleanup/log was missing or repeated")
	}
}

func assertFaultSiblingSucceeded(
	t *testing.T,
	service *automations.Service,
	record automations.Record,
	run automations.Run,
) {
	t.Helper()
	got := runEntry(historyEntry(t, service, record.ID, string(run.ID)))
	if automations.RunStateStatus(got.State) != automations.RunSucceeded {
		t.Fatalf("independent Run did not succeed: %#v", got)
	}
}

func assertExecutorFaultEvidence(
	t *testing.T,
	service *automations.Service,
	record automations.Record,
	run automations.Run,
	boundary string,
	commit, cleanupFails bool,
) {
	t.Helper()
	got := runEntry(historyEntry(t, service, record.ID, string(run.ID)))
	want := automations.RunInterrupted
	if cleanupFails {
		want = automations.RunRunning
	}
	if boundary == "run" && commit {
		want = automations.RunSucceeded
	}
	if automations.RunStateStatus(got.State) != want {
		t.Fatalf("fault Run status = %s, want %s", automations.RunStateStatus(got.State), want)
	}
	if want == automations.RunInterrupted &&
		(runFailure(got.State) == nil || *runFailure(got.State) != automations.FailureExecutorFault) {
		t.Fatalf("fault Run lost interruption reason: %#v", got)
	}
	if boundary != "run" && automations.StepAttemptStatus(got.Steps[1].State) != automations.StepNotAttempted {
		t.Fatal("uncertain progress advanced the next Step")
	}
	if boundary == "step" && commit &&
		(automations.StepAttemptStatus(got.Steps[0].State) != automations.StepSatisfied || stepVerified(got.Steps[0].State) == nil) {
		t.Fatal("cleanup overwrote committed terminal Step")
	}
	if boundary == "start" && stepVerified(got.Steps[0].State) != nil {
		t.Fatal("cleanup invented verified Command evidence")
	}
}

func assertExecutorCleanupBackstop(
	t *testing.T,
	service *automations.Service,
	record automations.Record,
	run automations.Run,
	writer *lockedAutomationLogWriter,
) {
	t.Helper()
	if len(automationLogEvents(writer.records(t), "automation.run_interruption_failed")) != 1 {
		t.Fatal("failed scoped cleanup was not logged once")
	}
	if _, err := service.StartManualRun(
		t.Context(),
		automations.ManualRunInput{AutomationID: record.ID},
	); !errors.Is(
		err,
		automations.ErrAutomationBusy,
	) {
		t.Fatalf("unfinished Automation busy error = %v", err)
	}
	if err := service.InterruptActiveRuns(t.Context(), runtimeTestNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got := runEntry(historyEntry(t, service, record.ID, string(run.ID)))
	if automations.RunStateStatus(got.State) != automations.RunInterrupted ||
		*runFailure(got.State) != automations.FailureCoreRestarted ||
		stepVerified(got.Steps[0].State) != nil {
		t.Fatalf("startup backstop invented completion: %#v", got)
	}
}
