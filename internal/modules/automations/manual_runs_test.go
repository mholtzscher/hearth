package automations_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Each accepted manual start creates a distinct snapshot, even when disabled.
func TestStartManualRunCreatesDistinctRunsEvenWhenDisabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	definition := runtimeDefinition(t, 2)
	definition.Enabled = false
	record := createRuntimeAutomation(t, service, definition)

	first, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)
	second, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

	if first.ID == second.ID {
		t.Fatalf("manual Runs share identity %s", first.ID)
	}
	for _, run := range []automations.Run{first, second} {
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
		if summary.Kind != automations.HistoryRun || summary.Status != automations.RunSucceeded {
			t.Fatalf("history summary = %#v", summary)
		}
	}
}

// A busy manual start must return automation_busy without writing history.
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

	if _, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID}); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := service.StartManualRun(
		ctx, automations.ManualRunInput{AutomationID: record.ID},
	); !errors.Is(err, automations.ErrAutomationBusy) {
		t.Fatalf("busy start error = %v, want ErrAutomationBusy", err)
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 || history[0].Kind != automations.HistoryRun {
		t.Fatalf("busy manual start wrote history: %#v", history)
	}
	close(gate)
	waitForRuns(t, service)
}

// Either closed admission gate must refuse the start without creating a Run.
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
			if _, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID}); !errors.Is(
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

// An unknown Automation must return ErrAutomationNotFound without writing history.
func TestStartManualRunUnknownAutomation(t *testing.T) {
	t.Parallel()
	service, _ := newRuntimeService(t, newScriptedDevices(), runtimeTestDependencies())
	missing, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.StartManualRun(
		context.Background(), automations.ManualRunInput{AutomationID: missing},
	); !errors.Is(err, automations.ErrAutomationNotFound) {
		t.Fatalf("unknown automation error = %v, want ErrAutomationNotFound", err)
	}
}
