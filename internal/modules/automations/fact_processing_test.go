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

// One fresh Fact starts all matching Automations; retained receipts prevent
// a republished duplicate from starting more Runs.
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

// Staleness takes precedence over busy when classifying a matching Fact.
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
	var skip automations.HistorySummary
	for _, summary := range history {
		if summary.Kind == automations.HistorySkip {
			skip = summary
		}
	}
	if skip.Reason != automations.SkipStaleFact {
		t.Fatalf("stale skip = %#v", skip)
	}
	entry := historyEntry(t, service, record.ID, skip.ID)
	if entry.Skip == nil || entry.Skip.Reason != automations.SkipStaleFact {
		t.Fatalf("skip detail = %#v", entry)
	}
	if len(entry.Skip.MatchedTriggers) != 1 || entry.Skip.MatchedTriggers[0].ID != "trigger" {
		t.Fatalf("skip matched triggers = %#v", entry.Skip.MatchedTriggers)
	}
	close(gate)
	waitForRuns(t, service)
}

// A fresh Fact matching a busy Automation records a Skip, not another Run.
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

// Contradictory Fact families must return ErrInvalidDeviceFact without writes.
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
