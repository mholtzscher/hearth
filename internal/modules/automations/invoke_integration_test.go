//go:build integration

package automations_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type invokeAdmissionObserver struct {
	automations.Repository

	rejected chan struct{}
}

type changingInvokeAdmission struct {
	automations.Repository

	change   func(context.Context) error
	attempts atomic.Int32
	result   chan error
}

func (repo *changingInvokeAdmission) AdmitInvokedRun(
	ctx context.Context,
	input automations.InvokeStepInput,
	snapshot devices.EntityStateSnapshot,
	at time.Time,
) (*automations.Run, error) {
	repo.attempts.Add(1)
	if err := repo.change(ctx); err != nil {
		repo.result <- err
		return nil, err
	}
	run, err := repo.Repository.AdmitInvokedRun(ctx, input, snapshot, at)
	repo.result <- err
	return run, err
}

func (repo *invokeAdmissionObserver) AdmitInvokedRun(
	ctx context.Context,
	input automations.InvokeStepInput,
	snapshot devices.EntityStateSnapshot,
	at time.Time,
) (*automations.Run, error) {
	run, err := repo.Repository.AdmitInvokedRun(ctx, input, snapshot, at)
	if err == nil && run == nil {
		close(repo.rejected)
	}
	return run, err
}

func TestInvokeExecutesChildAndRecordsReciprocalHistory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	service, database := newRuntimeService(t, newScriptedDevices(), runtimeTestDependencies())
	childDefinition := runtimeDefinition(t, 1)
	childDefinition.Triggers = nil
	childDefinition.Enabled = false
	child := createRuntimeAutomation(t, service, childDefinition)
	parentDefinition := runtimeDefinition(t, 1)
	parentDefinition.Steps = append(
		[]automations.Step{{ID: "invoke", Body: automations.InvokeStep{AutomationID: child.ID}}},
		parentDefinition.Steps...)
	parent := createRuntimeAutomation(t, service, parentDefinition)
	started, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)
	entry, err := service.GetHistoryEntry(ctx, parent.ID, string(started.ID))
	if err != nil {
		t.Fatal(err)
	}
	run := entry.(automations.Run)
	if automations.RunStateStatus(run.State) != automations.RunSucceeded || len(run.Executions) != 2 {
		t.Fatalf("parent = %#v", run)
	}
	details, ok := run.Executions[0].Details.(automations.InvokeExecutionDetails)
	if !ok {
		t.Fatalf("Invoke evidence = %#v", run.Executions[0])
	}
	succeeded, ok := details.State.(automations.InvokeSucceeded)
	if !ok {
		t.Fatalf("Invoke state = %#v", details.State)
	}
	childEntry, err := service.GetHistoryEntry(ctx, child.ID, string(succeeded.ChildRunID))
	if err != nil {
		t.Fatal(err)
	}
	childRun := childEntry.(automations.Run)
	cause, ok := childRun.Cause.(automations.InvocationCause)
	if !ok || cause.ParentAutomationID != parent.ID || cause.ParentRunID != started.ID ||
		cause.ParentStepID != "invoke" ||
		len(childRun.MatchedTriggerIDs) != 0 ||
		automations.RunStateStatus(childRun.State) != automations.RunSucceeded {
		t.Fatalf("child = %#v", childRun)
	}
	if _, err = database.ExecContext(
		ctx,
		`DELETE FROM automation_history WHERE id = ?`,
		string(childRun.ID),
	); err != nil {
		t.Fatal(err)
	}
	entry, err = service.GetHistoryEntry(ctx, parent.ID, string(started.ID))
	if err != nil || automations.RunStateStatus(entry.(automations.Run).State) != automations.RunSucceeded {
		t.Fatalf("parent after child expiry = %#v, %v", entry, err)
	}
}

func TestInvokeSaveRejectsMissingTargetInNestedArm(t *testing.T) {
	t.Parallel()
	service, _ := newRuntimeService(t, newScriptedDevices(), runtimeTestDependencies())
	missing, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	definition := runtimeDefinition(t, 1)
	definition.Steps = []automations.Step{{ID: "branch", Body: automations.IfStep{
		Conditions: automations.Condition{
			ID: "condition",
			Body: automations.ExpressionCondition{
				Value: automations.ValueBinding{Body: automations.ExpressionValue{Source: "true"}},
			},
		},
		Then: definition.Steps,
		Else: []automations.Step{{ID: "call", Body: automations.InvokeStep{AutomationID: missing}}},
	}}}
	_, err = service.CreateAutomation(t.Context(), definition)
	var invalid *automations.DefinitionError
	if !errors.As(err, &invalid) || len(invalid.Issues) != 1 ||
		invalid.Issues[0].Path != "/steps/0/else/0/automation_id" {
		t.Fatalf("error = %#v, want located missing target", err)
	}
}

func TestInvokeDefinitionChangeAfterSnapshotStopsWithoutRetry(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	scripted := newScriptedDevices()
	base := newAutomationRepository(t, openAutomationDatabase(t))
	repo := &changingInvokeAdmission{Repository: base, result: make(chan error, 1)}
	service := automations.NewService(repo, scripted, runtimeTestDependencies())
	first, added := newEntityID(t), newEntityID(t)
	childDefinition := runtimeDefinition(t, 1)
	childDefinition.Conditions = admissionConditionTree(first, "30")
	child := createRuntimeAutomation(t, service, childDefinition)
	parentDefinition := runtimeDefinition(t, 1)
	parentDefinition.Steps = append(
		[]automations.Step{{ID: "call", Body: automations.InvokeStep{AutomationID: child.ID}}},
		parentDefinition.Steps...,
	)
	parent := createRuntimeAutomation(t, service, parentDefinition)
	oldCondition := *childDefinition.Conditions
	addedCondition := *admissionConditionTree(added, "30")
	addedCondition.ID = "added"
	childDefinition.Conditions = &automations.Condition{
		ID: "both", Body: automations.AllCondition{Children: []automations.Condition{oldCondition, addedCondition}},
	}
	repo.change = func(ctx context.Context) error {
		_, err := base.ReplaceAutomation(ctx, child.ID, child.Revision, childDefinition)
		return err
	}
	scripted.setEntityStateSnapshot(admissionSnapshot(admissionState(t, first, `{"level":10}`, runtimeTestNow)))
	started, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	admissionErr := <-repo.result
	if !errors.Is(admissionErr, automations.ErrConditionSnapshotRequired) {
		t.Fatalf("child admission error = %v, want snapshot coverage error", admissionErr)
	}
	waitForRuns(t, service)
	if got := repo.attempts.Load(); got != 1 {
		t.Fatalf("child admission attempts = %d, want one", got)
	}
	requests := scripted.snapshotRequests()
	if len(requests) != 1 || !slices.Equal(requests[0], []devices.EntityID{first}) {
		t.Fatalf("State pre-reads = %v, want only original Condition Entity", requests)
	}
	if history := listHistory(t, service, child.ID); len(history) != 0 {
		t.Fatalf("child history = %#v, want no Run or Skip", history)
	}
	entry, err := service.GetHistoryEntry(ctx, parent.ID, string(started.ID))
	if err != nil {
		t.Fatal(err)
	}
	run := entry.(automations.Run)
	completed, ok := run.State.(automations.CompletedRun)
	if !ok || automations.RunOutcomeStatus(completed.Outcome) != automations.RunInterrupted ||
		automations.RunOutcomeFailureCode(completed.Outcome) == nil ||
		*automations.RunOutcomeFailureCode(completed.Outcome) != automations.FailureExecutorFault || len(run.Executions) != 0 {
		t.Fatalf("parent = %#v, want executor fault before Invoke reach", run)
	}
	if count := scripted.executionCount(); count != 0 {
		t.Fatalf("Commands executed = %d, want no child or downstream effect", count)
	}
}

func TestInvokeWaitsAndShutdownJoinsChild(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	scripted := newScriptedDevices()
	gate, started := make(chan struct{}), make(chan struct{})
	var once sync.Once
	scripted.block = gate
	scripted.onStart = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	child := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
	definition := runtimeDefinition(t, 1)
	definition.Steps = append(
		[]automations.Step{{ID: "call", Body: automations.InvokeStep{AutomationID: child.ID}}},
		definition.Steps...)
	parent := createRuntimeAutomation(t, service, definition)
	run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	entry, err := service.GetHistoryEntry(ctx, parent.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	active := entry.(automations.Run)
	if automations.RunStateStatus(active.State) != automations.RunRunning || len(active.Executions) != 1 {
		t.Fatalf("parent while child blocked = %#v", active)
	}
	if _, ok := active.Executions[0].Details.(automations.InvokeExecutionDetails).State.(automations.InvokeRunning); !ok {
		t.Fatalf("parent evidence = %#v", active.Executions)
	}
	service.StopAdmission()
	short, cancel := context.WithCancel(ctx)
	cancel()
	if err = service.Drain(short); err == nil {
		t.Fatal("Drain returned before blocked child finished")
	}
	close(gate)
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = service.Drain(deadline); err != nil {
		t.Fatal(err)
	}
	entry, err = service.GetHistoryEntry(ctx, parent.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	parentRun := entry.(automations.Run)
	if automations.RunStateStatus(parentRun.State) != automations.RunInterrupted || len(parentRun.Executions) != 1 {
		t.Fatalf("stopped parent = %#v", parentRun)
	}
	if _, ok := parentRun.Executions[0].Details.(automations.InvokeExecutionDetails).State.(automations.InvokeInterrupted); !ok {
		t.Fatalf("stopped evidence = %#v", parentRun.Executions)
	}
	scripted.mu.Lock()
	count := len(scripted.executions)
	scripted.mu.Unlock()
	if count != 1 {
		t.Fatalf("commands = %d, want only child", count)
	}
}

func TestInvokeWaitsForChildBeforeParentCommand(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	scripted := newScriptedDevices()
	gate, started := make(chan struct{}), make(chan struct{})
	var once sync.Once
	scripted.block = gate
	scripted.onStart = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	child := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
	definition := runtimeDefinition(t, 1)
	definition.Steps = append(
		[]automations.Step{{ID: "call", Body: automations.InvokeStep{AutomationID: child.ID}}},
		definition.Steps...)
	parent := createRuntimeAutomation(t, service, definition)
	run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	scripted.mu.Lock()
	count := len(scripted.executions)
	scripted.mu.Unlock()
	if count != 1 {
		t.Fatalf("commands before child completion = %d", count)
	}
	entry, err := service.GetHistoryEntry(ctx, parent.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.(automations.Run).Executions) != 1 {
		t.Fatalf("parent ran ahead: %#v", entry)
	}
	close(gate)
	waitForRuns(t, service)
	scripted.mu.Lock()
	count = len(scripted.executions)
	scripted.mu.Unlock()
	if count != 2 {
		t.Fatalf("commands after child completion = %d", count)
	}
	entry, err = service.GetHistoryEntry(ctx, parent.ID, string(run.ID))
	if err != nil || automations.RunStateStatus(entry.(automations.Run).State) != automations.RunSucceeded {
		t.Fatalf("completed parent = %#v, %v", entry, err)
	}
}

func TestInvokeCycleFailsViaBusyAdmission(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	service, _ := newRuntimeService(t, newScriptedDevices(), runtimeTestDependencies())
	a := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
	b := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
	a.Definition.Steps = []automations.Step{{ID: "to-b", Body: automations.InvokeStep{AutomationID: b.ID}}}
	b.Definition.Steps = []automations.Step{{ID: "to-a", Body: automations.InvokeStep{AutomationID: a.ID}}}
	if _, err := service.ReplaceAutomation(ctx, a.ID, a.Revision, a.Definition); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReplaceAutomation(ctx, b.ID, b.Revision, b.Definition); err != nil {
		t.Fatal(err)
	}
	run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)
	entry, err := service.GetHistoryEntry(ctx, a.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	parent := entry.(automations.Run)
	failure, ok := parent.Executions[0].Details.(automations.InvokeExecutionDetails).State.(automations.InvokeChildFailed)
	if !ok || automations.RunStateStatus(parent.State) != automations.RunFailed {
		t.Fatalf("parent = %#v", parent)
	}
	entry, err = service.GetHistoryEntry(ctx, b.ID, string(failure.ChildRunID))
	if err != nil {
		t.Fatal(err)
	}
	nested := entry.(automations.Run)
	rejected, ok := nested.Executions[0].Details.(automations.InvokeExecutionDetails).State.(automations.InvokeRejected)
	if !ok || rejected.ReasonCode != "automation_busy" || rejected.SkipID != nil {
		t.Fatalf("nested = %#v", nested)
	}
}

func TestSequentialInvocationsCreateDistinctChildRuns(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	service, _ := newRuntimeService(t, newScriptedDevices(), runtimeTestDependencies())
	child := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
	definition := runtimeDefinition(t, 1)
	definition.Steps = []automations.Step{
		{ID: "first", Body: automations.InvokeStep{AutomationID: child.ID}},
		{ID: "second", Body: automations.InvokeStep{AutomationID: child.ID}},
	}
	parent := createRuntimeAutomation(t, service, definition)
	started, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)
	entry, err := service.GetHistoryEntry(ctx, parent.ID, string(started.ID))
	if err != nil {
		t.Fatal(err)
	}
	run := entry.(automations.Run)
	if automations.RunStateStatus(run.State) != automations.RunSucceeded || len(run.Executions) != 2 {
		t.Fatalf("parent = %#v", run)
	}
	first := run.Executions[0].Details.(automations.InvokeExecutionDetails).State.(automations.InvokeSucceeded)
	second := run.Executions[1].Details.(automations.InvokeExecutionDetails).State.(automations.InvokeSucceeded)
	if first.ChildRunID == second.ChildRunID || len(listHistory(t, service, child.ID)) != 2 {
		t.Fatalf("children = %#v, %#v", first, second)
	}
}

func TestConcurrentCallersRejectBusyChild(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	scripted := newScriptedDevices()
	gate, started := make(chan struct{}), make(chan struct{})
	var once sync.Once
	scripted.block = gate
	scripted.onStart = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	deps := runtimeTestDependencies()
	database := openAutomationDatabase(t)
	repo := &invokeAdmissionObserver{
		Repository: automationssqlite.NewAutomationRepository(database, deps),
		rejected:   make(chan struct{}),
	}
	service := automations.NewService(repo, scripted, deps)
	child := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
	definition := runtimeDefinition(t, 1)
	definition.Steps = []automations.Step{{ID: "call", Body: automations.InvokeStep{AutomationID: child.ID}}}
	first := createRuntimeAutomation(t, service, definition)
	second := createRuntimeAutomation(t, service, definition)
	if _, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: first.ID}); err != nil {
		t.Fatal(err)
	}
	<-started
	secondRun, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: second.ID})
	if err != nil {
		t.Fatal(err)
	}
	<-repo.rejected
	entry, err := service.GetHistoryEntry(ctx, second.ID, string(secondRun.ID))
	if err != nil {
		t.Fatal(err)
	}
	retained := entry.(automations.Run)
	rejected, ok := retained.Executions[0].Details.(automations.InvokeExecutionDetails).State.(automations.InvokeRejected)
	if !ok || rejected.ReasonCode != "automation_busy" {
		t.Fatalf("second caller = %#v", retained)
	}
	close(gate)
	waitForRuns(t, service)
	if history := listHistory(t, service, child.ID); len(history) != 1 {
		t.Fatalf("child history = %#v", history)
	}
}

func TestInvokeCompletionFaultStopsParentWithoutReplayingChild(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	scripted := newScriptedDevices()
	service, database := newRuntimeService(t, scripted, runtimeTestDependencies())
	child := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
	definition := runtimeDefinition(t, 1)
	definition.Steps = append(
		[]automations.Step{{ID: "call", Body: automations.InvokeStep{AutomationID: child.ID}}},
		definition.Steps...)
	parent := createRuntimeAutomation(t, service, definition)
	if _, err := database.ExecContext(
		ctx,
		`CREATE TRIGGER fail_invoke_completion BEFORE UPDATE ON automation_step_executions WHEN NEW.kind='invoke' AND NEW.phase='succeeded' BEGIN SELECT RAISE(ABORT,'injected'); END`,
	); err != nil {
		t.Fatal(err)
	}
	run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)
	entry, err := service.GetHistoryEntry(ctx, parent.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	retained := entry.(automations.Run)
	if automations.RunStateStatus(retained.State) != automations.RunInterrupted || len(retained.Executions) != 1 {
		t.Fatalf("parent = %#v", retained)
	}
	interrupted, ok := retained.Executions[0].Details.(automations.InvokeExecutionDetails).State.(automations.InvokeInterrupted)
	if !ok || interrupted.ReasonCode != automations.FailureExecutorFault {
		t.Fatalf("Invoke = %#v", retained.Executions[0])
	}
	scripted.mu.Lock()
	count := len(scripted.executions)
	scripted.mu.Unlock()
	if count != 1 {
		t.Fatalf("commands = %d, want one child command", count)
	}
	children := listHistory(t, service, child.ID)
	if len(children) != 1 {
		t.Fatalf("child history = %#v", children)
	}
}
