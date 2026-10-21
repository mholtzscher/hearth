//go:build integration

package automations_test

import (
	"context"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
)

// A blocked run_started log must not keep a completed manual Run tracked.
func TestDrainCompletesWhileManualRunStartedLogBlocked(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	logger, writer, entered, unblock := newBlockedAdmissionLogger(t, runStartedLogMessage)
	dependencies := runtimeTestDependencies()
	dependencies.Logger = logger
	blocking := newBlockingAdmissionRepository(
		automationssqlite.NewAutomationRepository(openAutomationDatabase(t), dependencies),
	)
	service := automations.NewService(blocking, scripted, dependencies)
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))

	admitted := make(chan error, 1)
	go func() {
		_, err := service.StartManualRun(context.Background(), automations.ManualRunInput{AutomationID: record.ID})
		admitted <- err
	}()
	requireAdmissionInFlight(t, blocking)

	// Close mid-transaction so the Run drains without executing Commands.
	service.StopAdmission()
	close(blocking.release)

	requireBlockedDiagnostic(t, entered)
	requireDrainCompletesWhileLogBlocked(t, service)
	// The join must cover Run completion, including its unblocked diagnostic.
	requireDrainedRun(t, service, record.ID)
	requireLoggedEvents(t, writer, "automation.run_completed", 1)

	unblock()
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatalf("StartManualRun = %v, want a committed Run", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartManualRun did not return after the diagnostic sink was released")
	}
	requireLoggedEvents(t, writer, "automation.run_started", 1)
	if scripted.executionCount() != 0 {
		t.Fatalf("drain executed %d Commands, want 0", scripted.executionCount())
	}
}

// An admission that starts no Run must release its reservation before logging.
func TestDrainCompletesWhileSkippedLogBlocked(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	logger, writer, entered, unblock := newBlockedAdmissionLogger(t, skippedLogMessage)
	dependencies := runtimeTestDependencies()
	dependencies.Logger = logger
	service, _ := newRuntimeService(t, scripted, dependencies)
	entity := newEntityID(t)
	record := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))

	// A stale Fact records a Skip without starting a Run.
	stale := newObservationFact(t, entity, runtimeTestNow.Add(-automations.FactMaximumAge-time.Second))
	outcome := make(chan automations.AdmissionOutcome, 1)
	admitFailed := make(chan error, 1)
	go func() {
		result, err := service.ReceiveDeviceFact(context.Background(), stale)
		if err != nil {
			admitFailed <- err
			return
		}
		outcome <- result
	}()

	requireBlockedDiagnostic(t, entered)
	service.StopAdmission()
	requireDrainCompletesWhileLogBlocked(t, service)

	unblock()
	select {
	case err := <-admitFailed:
		t.Fatalf("ReceiveDeviceFact = %v, want a recorded stale_fact Skip", err)
	case result := <-outcome:
		if result.StartedRuns != 0 || result.RecordedSkips != 1 {
			t.Fatalf("admission outcome = %#v, want one skip and no Run", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReceiveDeviceFact did not return after the diagnostic sink was released")
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 {
		t.Fatalf("history = %#v, want one Skip", history)
	}
	body, ok := history[0].Body.(automations.SkipHistorySummary)
	if !ok || body.Reason != automations.SkipStaleFact {
		t.Fatalf("automation %s history = %#v, want one stale_fact Skip", record.ID, history)
	}
	requireLoggedEvents(t, writer, "automation.skipped", 1)
	requireLoggedEvents(t, writer, "automation.run_started", 0)
	if scripted.executionCount() != 0 {
		t.Fatalf("skipped admission executed %d Commands, want 0", scripted.executionCount())
	}
}

// All committed Runs need workers before the first run_started log can block.
func TestDrainCompletesWhileFanOutRunStartedLogBlocked(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	logger, writer, entered, unblock := newBlockedAdmissionLogger(t, runStartedLogMessage)
	dependencies := runtimeTestDependencies()
	dependencies.Logger = logger
	blocking := newBlockingAdmissionRepository(
		automationssqlite.NewAutomationRepository(openAutomationDatabase(t), dependencies),
	)
	service := automations.NewService(blocking, scripted, dependencies)
	entity := newEntityID(t)
	first := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))
	second := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))

	admitted := make(chan error, 1)
	go func() {
		_, err := service.ReceiveDeviceFact(context.Background(), newObservationFact(t, entity, runtimeTestNow))
		admitted <- err
	}()
	requireAdmissionInFlight(t, blocking)

	service.StopAdmission()
	close(blocking.release)

	requireBlockedDiagnostic(t, entered)
	requireDrainCompletesWhileLogBlocked(t, service)
	// A stranded second Run would still be Running, not interrupted.
	requireDrainedRun(t, service, first.ID)
	requireDrainedRun(t, service, second.ID)
	// Completion logs must pass the blocked admission log.
	requireLoggedEvents(t, writer, "automation.run_completed", 2)

	unblock()
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatalf("ReceiveDeviceFact = %v, want a committed fan-out admission", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReceiveDeviceFact did not return after the diagnostic sink was released")
	}
	requireLoggedEvents(t, writer, "automation.run_started", 2)
	if scripted.executionCount() != 0 {
		t.Fatalf("drain executed %d Commands, want 0", scripted.executionCount())
	}
}
