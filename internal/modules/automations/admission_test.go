package automations_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// TestStartManualRunCreatesDistinctRunsEvenWhenDisabled protects A4: each
// accepted POST creates a distinct snapshotted Run with manual provenance, even
// for a disabled Automation. It fails if callers share Runs or if disabled
// Automations cannot be started manually.
func TestStartManualRunCreatesDistinctRunsEvenWhenDisabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	definition := runtimeDefinition(t, 2)
	definition.Enabled = false
	record := createRuntimeAutomation(t, service, definition)

	first, err := service.StartManualRun(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)
	second, err := service.StartManualRun(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

	if first.ID == second.ID {
		t.Fatalf("manual Runs share identity %s", first.ID)
	}
	for _, run := range []automations.AutomationRun{first, second} {
		if run.Source != automations.RunSourceManual {
			t.Fatalf("run source = %q, want manual", run.Source)
		}
		if run.Fact != nil || len(run.MatchedTriggerIDs) != 0 {
			t.Fatalf("manual run carries fact provenance: %#v", run)
		}
		if run.Revision != record.Revision || run.AutomationName != record.Definition.Name {
			t.Fatalf("run snapshot metadata = %#v", run)
		}
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 2 {
		t.Fatalf("history entries = %d, want 2", len(history))
	}
	for _, summary := range history {
		if summary.Kind != automations.AutomationHistoryRun || summary.Status != automations.RunSucceeded {
			t.Fatalf("history summary = %#v", summary)
		}
	}
}

// TestStartManualRunBusyReturns409WithoutHistory protects A4: a manual start
// while that Automation already has a running Run returns automation_busy and
// writes no Skip or extra Run.
func TestStartManualRunBusyReturns409WithoutHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	scripted.block = gate
	scripted.onStart = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))

	if _, err := service.StartManualRun(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := service.StartManualRun(ctx, record.ID); !errors.Is(err, automations.ErrAutomationBusy) {
		t.Fatalf("busy start error = %v, want ErrAutomationBusy", err)
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 || history[0].Kind != automations.AutomationHistoryRun {
		t.Fatalf("busy manual start wrote history: %#v", history)
	}
	close(gate)
	waitForRuns(t, service)
}

// TestStartManualRunRefusesClosedAdmission protects A4: a closed automation gate
// or a closed Command gate returns admission_unavailable and creates no Run.
func TestStartManualRunRefusesClosedAdmission(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, test := range []struct {
		name   string
		closer func(*automations.Service, *scriptedDevices)
	}{
		{"automation admission", func(service *automations.Service, _ *scriptedDevices) {
			service.StopAdmission()
		}},
		{"command admission", func(_ *automations.Service, scripted *scriptedDevices) {
			scripted.setCommandAdmissionOpen(false)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			scripted := newScriptedDevices()
			service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
			record := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
			test.closer(service, scripted)
			if _, err := service.StartManualRun(ctx, record.ID); !errors.Is(
				err, automations.ErrAdmissionUnavailable,
			) {
				t.Fatalf("closed admission error = %v, want ErrAdmissionUnavailable", err)
			}
			if history := listHistory(t, service, record.ID); len(history) != 0 {
				t.Fatalf("closed admission wrote history: %#v", history)
			}
		})
	}
}

// TestStartManualRunUnknownAutomation protects A4: an unknown Automation ID
// returns ErrAutomationNotFound and writes no history.
func TestStartManualRunUnknownAutomation(t *testing.T) {
	t.Parallel()
	service, _ := newRuntimeService(t, newScriptedDevices(), runtimeTestDependencies())
	missing, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.StartManualRun(context.Background(), missing); !errors.Is(
		err, automations.ErrAutomationNotFound,
	) {
		t.Fatalf("unknown automation error = %v, want ErrAutomationNotFound", err)
	}
}

// TestReceiveDeviceFactFanOutAndDedupe protects admission grouping and the
// retained (fact_id, automation_id) receipt: one fresh Fact starts all matching
// Automations, and a republished duplicate starts no second Run.
func TestReceiveDeviceFactFanOutAndDedupe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	entity := newEntityID(t)
	first := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))
	second := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))
	fact := newObservationFact(t, entity, runtimeTestNow)

	outcome, err := service.ReceiveDeviceFact(ctx, fact)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.MatchedAutomations != 2 || outcome.StartedRuns != 2 ||
		outcome.RecordedSkips != 0 || outcome.DuplicateOutcomes != 0 {
		t.Fatalf("first admission outcome = %#v", outcome)
	}
	waitForRuns(t, service)

	duplicate, err := service.ReceiveDeviceFact(ctx, fact)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.DuplicateOutcomes != 2 || duplicate.StartedRuns != 0 {
		t.Fatalf("duplicate admission outcome = %#v", duplicate)
	}
	if scripted.executionCount() != 2 {
		t.Fatalf("executions = %d, want 2", scripted.executionCount())
	}
	for _, id := range []automations.AutomationID{first.ID, second.ID} {
		history := listHistory(t, service, id)
		if len(history) != 1 || history[0].Status != automations.RunSucceeded {
			t.Fatalf("automation %s history = %#v", id, history)
		}
	}
}

// TestReceiveDeviceFactStaleBeforeBusy protects freshness precedence: an old Fact
// records stale_fact rather than automation_busy even while a Run is active.
func TestReceiveDeviceFactStaleBeforeBusy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	scripted.block = gate
	scripted.onStart = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	entity := newEntityID(t)
	record := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))

	fresh := newObservationFact(t, entity, runtimeTestNow)
	if _, err := service.ReceiveDeviceFact(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	<-started

	stale := newObservationFact(t, entity, runtimeTestNow.Add(-31*time.Second))
	outcome, err := service.ReceiveDeviceFact(ctx, stale)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.RecordedSkips != 1 || outcome.StartedRuns != 0 {
		t.Fatalf("stale admission outcome = %#v", outcome)
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 2 {
		t.Fatalf("history entries = %d, want run and skip", len(history))
	}
	var skip automations.AutomationHistorySummary
	for _, summary := range history {
		if summary.Kind == automations.AutomationHistorySkip {
			skip = summary
		}
	}
	if skip.Reason != automations.AutomationSkipStaleFact {
		t.Fatalf("stale skip = %#v", skip)
	}
	entry := historyEntry(t, service, record.ID, skip.ID)
	if entry.Skip == nil || entry.Skip.Reason != automations.AutomationSkipStaleFact {
		t.Fatalf("skip detail = %#v", entry)
	}
	if len(entry.Skip.MatchedTriggers) != 1 || entry.Skip.MatchedTriggers[0].ID != "trigger" {
		t.Fatalf("skip matched triggers = %#v", entry.Skip.MatchedTriggers)
	}
	close(gate)
	waitForRuns(t, service)
}

// TestReceiveDeviceFactBusyRecordsSkip protects the busy guard: a second fresh
// Fact for one already-running Automation writes automation_busy and no Run.
func TestReceiveDeviceFactBusyRecordsSkip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	scripted.block = gate
	scripted.onStart = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	entity := newEntityID(t)
	createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))

	if _, err := service.ReceiveDeviceFact(
		ctx, newObservationFact(t, entity, runtimeTestNow),
	); err != nil {
		t.Fatal(err)
	}
	<-started
	outcome, err := service.ReceiveDeviceFact(
		ctx, newObservationFact(t, entity, runtimeTestNow),
	)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.RecordedSkips != 1 || outcome.StartedRuns != 0 {
		t.Fatalf("busy admission outcome = %#v", outcome)
	}
	close(gate)
	waitForRuns(t, service)
}

// TestReceiveDeviceFactRejectsMalformedFact protects the admission input
// boundary: a contradictory family payload is ErrInvalidDeviceFact and writes
// nothing.
func TestReceiveDeviceFactRejectsMalformedFact(t *testing.T) {
	t.Parallel()
	service, _ := newRuntimeService(t, newScriptedDevices(), runtimeTestDependencies())
	if _, err := service.ReceiveDeviceFact(context.Background(), automations.DeviceFact{
		Family: automations.DeviceFactEntityEvent,
		Observation: &automations.ObservationFact{
			FactID: "fct_not-canonical",
		},
	}); !errors.Is(err, automations.ErrInvalidDeviceFact) {
		t.Fatalf("malformed fact error = %v, want ErrInvalidDeviceFact", err)
	}
}
