//go:build integration

package automations_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// This test protects the strict cutoff through the real SQLite repository and
// fails if the boundary row is deleted with the older history.
func TestPruneHistoryKeepsHistoryAtTheCutoffBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _ := newRuntimeService(t, newScriptedDevices(), runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, newEntityID(t)))
	if _, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID}); err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)
	if history := listHistory(t, service, record.ID); len(history) != 1 {
		t.Fatalf("terminal history = %d rows, want 1", len(history))
	}

	// The Run recorded itself at runtimeTestNow, so one window later the cutoff
	// lands exactly on recorded_at and strict-before must retain the row.
	if err := service.PruneHistory(
		ctx, runtimeTestNow.Add(runtimeTestHistoryRetention),
	); err != nil {
		t.Fatal(err)
	}
	if history := listHistory(t, service, record.ID); len(history) != 1 {
		t.Fatalf("history at the cutoff = %d rows, want 1", len(history))
	}

	// One nanosecond past the boundary is strictly older and must be pruned.
	if err := service.PruneHistory(
		ctx, runtimeTestNow.Add(runtimeTestHistoryRetention+time.Nanosecond),
	); err != nil {
		t.Fatal(err)
	}
	if history := listHistory(t, service, record.ID); len(history) != 0 {
		t.Fatalf("history past the cutoff = %d rows, want 0", len(history))
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
	if len(running) != 1 || running[0].Body.(automations.RunHistorySummary).Status != automations.RunRunning {
		t.Fatalf("running history was pruned: %#v", running)
	}
	var receipts int
	if err := database.QueryRowContext(
		ctx, `SELECT count(*) FROM automation_fact_receipts WHERE fact_id = ? AND automation_id = ?`,
		string(fact.FactID), string(factAutomation.ID),
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
