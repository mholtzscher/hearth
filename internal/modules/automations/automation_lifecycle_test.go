package automations //nolint:testpackage // Tests the concrete admission gate, worker drain, and retention transactions.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Same-key callers register exactly one worker, while another Automation is
// allowed to use the same Entity without waiting for that active Run.
func TestAutomationConcurrentServiceAdmission(t *testing.T) {
	t.Parallel()
	harness := newAutomationExecutionHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	secondEntered := make(chan struct{})
	defer close(release)
	harness.beforeSend = func(context.Context, devices.CommandRequest) error {
		if harness.sends.Load() == 1 {
			close(entered)
			<-release
		} else if harness.sends.Load() == 2 {
			close(secondEntered)
		}
		return nil
	}
	first := createExecutionAutomation(t, harness, harness.definition())
	definition := harness.definition()
	definition.Steps = definition.Steps[:1]
	independent := createExecutionAutomation(t, harness, definition)
	initial := startExecutionAutomation(t, harness, first.ID, "same-key")
	receiveAutomationBarrier(t, entered)
	const callers = 12
	var workers sync.WaitGroup
	for range callers {
		workers.Go(func() {
			admission, err := harness.service.StartManualRun(
				t.Context(),
				AutomationManualRequest{AutomationID: first.ID, IdempotencyKey: "same-key"},
			)
			if err != nil || !admission.Reused || admission.Run.ID != initial.Run.ID {
				t.Errorf("same-key admission = %#v, %v", admission, err)
			}
		})
	}
	workers.Wait()
	if harness.sends.Load() != 1 {
		t.Fatal("same-key retry registered duplicate work")
	}
	if _, err := harness.service.StartManualRun(
		t.Context(),
		AutomationManualRequest{AutomationID: first.ID, IdempotencyKey: "different-key"},
	); !errors.Is(
		err,
		ErrAutomationRunActive,
	) {
		t.Fatalf("different-key overlap = %v", err)
	}
	second := startExecutionAutomation(t, harness, independent.ID, "independent")
	receiveAutomationBarrier(t, secondEntered)
	if harness.sends.Load() != 2 {
		t.Fatal("independent automation did not overlap")
	}
	release <- struct{}{}
	firstRun := waitExecutionRun(t, harness, initial.Run.ID)
	secondRun, err := harness.service.GetAutomationRun(t.Context(), second.Run.ID)
	if err != nil || secondRun.Status != AutomationRunStatusSucceeded {
		t.Fatalf("independent run = %#v, %v", secondRun, err)
	}
	if firstRun.Status != AutomationRunStatusSucceeded || harness.sends.Load() != 3 {
		t.Fatalf("first run = %#v", firstRun)
	}
}

// Stop cannot pass a Step intent transaction already admitted under the gate.
// That Step is registered in a worker before shutdown can begin draining it.
func TestAutomationShutdownSerializesWithStepIntent(t *testing.T) {
	t.Parallel()
	harness := newAutomationExecutionHarness(t)
	generating, releaseIdentity := make(chan struct{}), make(chan struct{})
	defer close(releaseIdentity)
	harness.service.newCommandID = func() (devices.CommandID, error) { close(generating); <-releaseIdentity; return devices.NewCommandID() }
	definition := harness.definition()
	definition.Steps = definition.Steps[:1]
	automation := createExecutionAutomation(t, harness, definition)
	admission := startExecutionAutomation(t, harness, automation.ID, "intent-gate")
	receiveAutomationBarrier(t, generating)
	stopping, stopped := make(chan struct{}), make(chan struct{})
	go func() { close(stopping); harness.service.StopAutomationExecutionAdmission(); close(stopped) }()
	receiveAutomationBarrier(t, stopping)
	select {
	case <-stopped:
		t.Fatal("shutdown passed the admitted intent gate")
	default:
	}
	releaseIdentity <- struct{}{}
	receiveAutomationBarrier(t, stopped)
	run := waitExecutionRun(t, harness, admission.Run.ID)
	if run.Status != AutomationRunStatusSucceeded || run.Steps[0].CommandID == nil || harness.sends.Load() != 1 {
		t.Fatalf("admitted intent did not drain: %#v", run)
	}
}

// Retention continues across 500-row batches, preserves cutoff equality and
// active claims, and removes retained idempotency keys with their terminal Runs.
func TestAutomationServiceRetentionSweepsAllBatches(t *testing.T) {
	t.Parallel()
	harness := newAutomationExecutionHarness(t)
	automation := createExecutionAutomation(t, harness, harness.definition())
	for index := range 501 {
		run := admitTestAutomation(t, harness.repo, automation.ID, fmt.Sprintf("old-%d", index))
		interruptTestAutomation(t, harness.repo, run.ID)
	}
	cutoff := harness.repo.now().Add(time.Nanosecond)
	equalityRun := admitTestAutomation(t, harness.repo, automation.ID, "equality")
	harness.repo.now = func() time.Time { return cutoff }
	interruptTestAutomation(t, harness.repo, equalityRun.ID)
	active := admitTestAutomation(t, harness.repo, automation.ID, "active")
	if err := harness.service.PruneAutomationHistory(t.Context(), cutoff); err != nil {
		t.Fatal(err)
	}
	page, err := harness.service.ListAutomationRuns(t.Context(), AutomationRunListParams{})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("retention left batches or pruned protected rows: %#v, %v", page, err)
	}
	for _, run := range page.Items {
		if run.ID != equalityRun.ID && run.ID != active.ID {
			t.Fatalf("unexpected retained run: %#v", run)
		}
	}
	interruptTestAutomation(t, harness.repo, active.ID)
	fresh := startExecutionAutomation(t, harness, automation.ID, "old-0")
	if fresh.Reused {
		t.Fatal("pruned history retained idempotency key")
	}
	waitExecutionRun(t, harness, fresh.Run.ID)
}
