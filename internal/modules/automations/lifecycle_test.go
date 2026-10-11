package automations_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestDrainClosesIdleAutomationAdmission(t *testing.T) {
	t.Parallel()
	service, _ := newRuntimeService(t, newScriptedDevices(), runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
	for range 2 {
		if err := service.Drain(t.Context()); err != nil {
			t.Fatalf("idle Drain = %v", err)
		}
		if service.AdmissionOpen() {
			t.Fatal("idle Drain left admission open")
		}
	}
	if _, err := service.StartManualRun(
		t.Context(), automations.ManualRunInput{AutomationID: record.ID},
	); !errors.Is(err, automations.ErrAdmissionUnavailable) {
		t.Fatalf("manual admission after idle Drain = %v", err)
	}
	_, factErr := service.ReceiveDeviceFact(t.Context(), nil)
	if !errors.Is(factErr, automations.ErrAdmissionUnavailable) {
		t.Fatalf("Fact admission after idle Drain = %v", factErr)
	}
}

// Caller cancellation must not prevent an admitted Run from reaching verified completion.
func TestRunSurvivesCallerCancellation(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	scripted.block = gate
	scripted.onStart = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))

	ctx, cancel := context.WithCancel(context.Background())
	run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	close(gate)
	waitForRuns(t, service)

	entry := historyEntry(t, service, record.ID, string(run.ID))
	if runEntry(entry) == nil || automations.RunStateStatus(runEntry(entry).State) != automations.RunSucceeded {
		t.Fatalf("Run after caller cancellation = %#v, want succeeded", runEntry(entry))
	}
	if scripted.executionCount() != 1 {
		t.Fatalf("executions = %d, want 1", scripted.executionCount())
	}
}

// Deleting a definition must preserve its active snapshot and queryable history.
func TestDeletingDefinitionLetsActiveRunContinue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	scripted.block = gate
	scripted.onStart = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 2))

	run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err = service.DeleteAutomation(ctx, record.ID, record.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = service.GetAutomation(ctx, record.ID); err == nil {
		t.Fatal("definition survived deletion")
	}
	close(gate)
	waitForRuns(t, service)

	entry := historyEntry(t, service, record.ID, string(run.ID))
	if runEntry(entry) == nil || automations.RunStateStatus(runEntry(entry).State) != automations.RunSucceeded {
		t.Fatalf("deleted-definition Run = %#v, want succeeded", runEntry(entry))
	}
	if runEntry(entry).AutomationName != record.Definition.Name || runEntry(entry).Revision != record.Revision {
		t.Fatalf("retained snapshot lost definition identity: %#v", runEntry(entry))
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 {
		t.Fatalf("retained history = %#v", history)
	}
}

// Startup recovery must mark unfinished work interrupted/core_restarted without replay.
func TestInterruptActiveRunsClassifiesUnfinishedWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	database := openAutomationDatabase(t)
	repository := automationssqlite.NewAutomationRepository(database, dependencies)
	service := automations.NewService(repository, scripted, dependencies)
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 2))

	// Admit a Run with no worker, exactly like a Run left running by a crash.
	admitted, err := repository.AdmitManualRun(
		ctx, automations.ManualRunInput{AutomationID: record.ID}, devices.EntityStateSnapshot{}, runtimeTestNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	if runEntry(admitted) == nil {
		t.Fatal("manual admission did not commit a Run")
	}
	run := *runEntry(admitted)
	if err = service.InterruptActiveRuns(ctx, runtimeTestNow); err != nil {
		t.Fatal(err)
	}
	entry := historyEntry(t, service, record.ID, string(run.ID))
	if runEntry(entry) == nil || automations.RunStateStatus(runEntry(entry).State) != automations.RunInterrupted ||
		runFailure(
			runEntry(entry).State,
		) == nil || *runFailure(runEntry(entry).State) != automations.FailureCoreRestarted {
		t.Fatalf("interrupted Run = %#v", runEntry(entry))
	}
	for position, step := range runEntry(entry).Steps {
		if automations.StepAttemptStatus(step.State) != automations.StepNotAttempted {
			t.Fatalf("Step %d = %#v, want not_attempted", position, step)
		}
	}
	if scripted.executionCount() != 0 {
		t.Fatalf("interruption replayed %d Commands", scripted.executionCount())
	}
	if !service.AdmissionOpen() {
		t.Fatal("startup interruption closed admission")
	}
}

// Drain must let the current Command finish, then interrupt the next Step
// without reserving or linking another Command.
func TestCanceledDrainStopsRunBeforeNextStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	scripted.block = gate
	scripted.onStart = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 2))

	run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	drainContext, cancelDrain := context.WithCancel(ctx)
	cancelDrain()
	if err = service.Drain(drainContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Drain = %v, want context.Canceled", err)
	}
	if service.AdmissionOpen() {
		t.Fatal("canceled Drain left admission open")
	}
	if _, err = service.StartManualRun(
		ctx, automations.ManualRunInput{AutomationID: record.ID},
	); !errors.Is(err, automations.ErrAdmissionUnavailable) {
		t.Fatalf("manual admission after Drain = %v", err)
	}
	_, err = service.ReceiveDeviceFact(ctx, nil)
	if !errors.Is(err, automations.ErrAdmissionUnavailable) {
		t.Fatalf("Fact admission after Drain = %v", err)
	}
	waiting, cancelWait := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancelWait()
	if err = service.Drain(waiting); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain with a blocked Command = %v, want context.DeadlineExceeded", err)
	}
	close(gate)
	draining, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = service.Drain(draining); err != nil {
		t.Fatalf("Drain after Command completion = %v", err)
	}
	if err = service.Drain(draining); err != nil {
		t.Fatalf("repeated Drain = %v", err)
	}

	entry := historyEntry(t, service, record.ID, string(run.ID))
	if runEntry(entry) == nil || automations.RunStateStatus(runEntry(entry).State) != automations.RunInterrupted ||
		runFailure(
			runEntry(entry).State,
		) == nil || *runFailure(runEntry(entry).State) != automations.FailureCoreStopping {
		t.Fatalf("drained Run = %#v", runEntry(entry))
	}
	if automations.StepAttemptStatus(runEntry(entry).Steps[0].State) != automations.StepSatisfied {
		t.Fatalf("in-flight Step = %#v, want satisfied", runEntry(entry).Steps[0])
	}
	next := runEntry(entry).Steps[1]
	if automations.StepAttemptStatus(next.State) != automations.StepInterrupted || stepVerified(next.State) != nil ||
		stepFailureCode(next.State) == nil || *stepFailureCode(next.State) != automations.FailureCoreStopping {
		t.Fatalf("next Step = %#v, want interrupted/core_stopping with no link", next)
	}
	if scripted.executionCount() != 1 {
		t.Fatalf("drained executions = %d, want 1", scripted.executionCount())
	}
}
