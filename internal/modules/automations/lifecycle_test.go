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
	_, factErr := service.ReceiveDeviceFact(t.Context(), automations.DeviceFact{})
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
	if entry.Run == nil || entry.Run.Status != automations.RunSucceeded {
		t.Fatalf("Run after caller cancellation = %#v, want succeeded", entry.Run)
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
	if entry.Run == nil || entry.Run.Status != automations.RunSucceeded {
		t.Fatalf("deleted-definition Run = %#v, want succeeded", entry.Run)
	}
	if entry.Run.AutomationName != record.Definition.Name || entry.Run.Revision != record.Revision {
		t.Fatalf("retained snapshot lost definition identity: %#v", entry.Run)
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
	if admitted.Run == nil {
		t.Fatal("manual admission did not commit a Run")
	}
	run := *admitted.Run
	if err = service.InterruptActiveRuns(ctx, runtimeTestNow); err != nil {
		t.Fatal(err)
	}
	entry := historyEntry(t, service, record.ID, string(run.ID))
	if entry.Run == nil || entry.Run.Status != automations.RunInterrupted ||
		entry.Run.FailureCode == nil || *entry.Run.FailureCode != automations.FailureCoreRestarted {
		t.Fatalf("interrupted Run = %#v", entry.Run)
	}
	for position, step := range entry.Run.Steps {
		if step.Status != automations.StepNotAttempted {
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
	_, err = service.ReceiveDeviceFact(ctx, automations.DeviceFact{})
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
	if entry.Run == nil || entry.Run.Status != automations.RunInterrupted ||
		entry.Run.FailureCode == nil || *entry.Run.FailureCode != automations.FailureCoreStopping {
		t.Fatalf("drained Run = %#v", entry.Run)
	}
	if entry.Run.Steps[0].Status != automations.StepSatisfied {
		t.Fatalf("in-flight Step = %#v, want satisfied", entry.Run.Steps[0])
	}
	next := entry.Run.Steps[1]
	if next.Status != automations.StepInterrupted || next.VerifiedCommandID != nil ||
		next.FailureCode == nil || *next.FailureCode != automations.FailureCoreStopping {
		t.Fatalf("next Step = %#v, want interrupted/core_stopping with no link", next)
	}
	if scripted.executionCount() != 1 {
		t.Fatalf("drained executions = %d, want 1", scripted.executionCount())
	}
}

// Retention must remove only old terminal history, preserving active Runs and Fact receipts.
func TestPruneHistoryKeepsRunningRunsAndFactReceipts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	service, database := newRuntimeService(t, scripted, runtimeTestDependencies())

	// A terminal fact-backed Run eligible for pruning.
	factEntity := newEntityID(t)
	factAutomation := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, factEntity))
	fact := newObservationFact(t, factEntity, runtimeTestNow)
	if _, err := service.ReceiveDeviceFact(ctx, fact); err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

	// A running Run that pruning must never select.
	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	scripted.block = gate
	scripted.onStart = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	runningAutomation := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, newEntityID(t)))
	if _, err := service.StartManualRun(
		ctx, automations.ManualRunInput{AutomationID: runningAutomation.ID},
	); err != nil {
		t.Fatal(err)
	}
	<-started

	// A sweep one hour past the retention window makes the fixture's own
	// terminal Run eligible while leaving the gated Run running.
	sweepTime := runtimeTestNow.Add(runtimeTestHistoryRetention + time.Hour)
	if err := service.PruneHistory(ctx, sweepTime); err != nil {
		t.Fatal(err)
	}
	if history := listHistory(t, service, factAutomation.ID); len(history) != 0 {
		t.Fatalf("terminal history was not pruned: %#v", history)
	}
	running := listHistory(t, service, runningAutomation.ID)
	if len(running) != 1 || running[0].Status != automations.RunRunning {
		t.Fatalf("running history was pruned: %#v", running)
	}
	var receipts int
	if err := database.QueryRowContext(
		ctx, `SELECT count(*) FROM automation_fact_receipts WHERE fact_id = ? AND automation_id = ?`,
		string(fact.Observation.FactID), string(factAutomation.ID),
	).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("matched-Fact receipts = %d, want 1", receipts)
	}
	if _, err := service.ReceiveDeviceFact(ctx, fact); err != nil {
		t.Fatal(err)
	}
	if scripted.executionCount() != 2 {
		t.Fatalf("executions = %d, want 2 (no post-prune re-execution)", scripted.executionCount())
	}
	close(gate)
	waitForRuns(t, service)
}
