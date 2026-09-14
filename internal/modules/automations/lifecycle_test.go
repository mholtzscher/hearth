package automations_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// TestRunSurvivesCallerCancellation protects A7: canceling the admitted
// caller's context does not cancel detached work; the Run still reaches its
// verified terminal state.
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
	run, err := service.StartManualRun(ctx, record.ID)
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

// TestDeletingDefinitionLetsActiveRunContinue protects A7: hard-deleting a
// definition leaves an active snapshotted Run running and keeps its history
// queryable by the former Automation ID.
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

	run, err := service.StartManualRun(ctx, record.ID)
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

// TestInterruptActiveRunsClassifiesUnfinishedWork protects A7/A13: startup
// interruption marks running Runs and Steps interrupted/core_restarted without
// replaying any Command.
func TestInterruptActiveRunsClassifiesUnfinishedWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	database := openAutomationDatabase(t)
	repository := automations.NewSQLiteRepository(database, dependencies)
	service := automations.NewService(repository, scripted, dependencies)
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 2))

	// Admit a Run with no worker, exactly like a Run left running by a crash.
	run, err := repository.AdmitManualRun(ctx, record.ID, runtimeTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.InterruptActiveRuns(ctx, runtimeTestNow); err != nil {
		t.Fatal(err)
	}
	entry := historyEntry(t, service, record.ID, string(run.ID))
	if entry.Run == nil || entry.Run.Status != automations.RunInterrupted ||
		entry.Run.FailureCode == nil || *entry.Run.FailureCode != automations.AutomationFailureCoreRestarted {
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

// TestDrainStopsRunBeforeNextStep protects A7/A13: closing admission while a
// Command is in flight lets that Command finish, then marks the next Step and
// the Run interrupted/core_stopping with no Command link.
func TestDrainStopsRunBeforeNextStep(t *testing.T) {
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

	run, err := service.StartManualRun(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	service.StopAdmission()
	close(gate)
	waitForRuns(t, service)

	entry := historyEntry(t, service, record.ID, string(run.ID))
	if entry.Run == nil || entry.Run.Status != automations.RunInterrupted ||
		entry.Run.FailureCode == nil || *entry.Run.FailureCode != automations.AutomationFailureCoreStopping {
		t.Fatalf("drained Run = %#v", entry.Run)
	}
	if entry.Run.Steps[0].Status != automations.StepSatisfied {
		t.Fatalf("in-flight Step = %#v, want satisfied", entry.Run.Steps[0])
	}
	next := entry.Run.Steps[1]
	if next.Status != automations.StepInterrupted || next.VerifiedCommandID != nil ||
		next.FailureCode == nil || *next.FailureCode != automations.AutomationFailureCoreStopping {
		t.Fatalf("next Step = %#v, want interrupted/core_stopping with no link", next)
	}
	if scripted.executionCount() != 1 {
		t.Fatalf("drained executions = %d, want 1", scripted.executionCount())
	}
}

// TestPruneHistoryKeepsRunningRunsAndFactReceipts protects A7/A9: hourly
// pruning removes only terminal history older than the cutoff and never removes
// matched-Fact receipts.
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
	if _, err := service.StartManualRun(ctx, runningAutomation.ID); err != nil {
		t.Fatal(err)
	}
	<-started

	cutoff := runtimeTestNow.Add(time.Second)
	if _, err := service.PruneHistory(ctx, cutoff, 10); err != nil {
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
