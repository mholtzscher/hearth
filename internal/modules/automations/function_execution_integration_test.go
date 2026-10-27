//go:build integration

package automations_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

//nolint:gocognit // Each outcome checks the durable link, exact payload, and downstream effects together.
func TestFunctionExecutionRetainedOutcomesGateCommands(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		outcome  adapters.Evidence
		want     automations.RunStatus
		commands int
	}{
		{"success", adapters.Succeeded{Result: json.RawMessage(`{"ok":true}`)}, automations.RunSucceeded, 1},
		{"failed", adapters.Failed{ReasonCode: "provider_refused"}, automations.RunFailed, 0},
		{"not executed", adapters.NotExecuted{ReasonCode: "adapter_busy"}, automations.RunFailed, 0},
		{"unknown", adapters.Unknown{ReasonCode: "transport_error"}, automations.RunFailed, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stub := &scriptedFunctions{outcome: test.outcome}
			dependencies := runtimeTestDependencies()
			dependencies.Functions = stub
			scripted := newScriptedDevices()
			service, _ := newRuntimeService(t, scripted, dependencies)
			definition := runtimeDefinition(t, 1)
			definition.Steps = append([]automations.Step{functionNode(t, "inspect")}, definition.Steps...)
			record := createRuntimeAutomation(t, service, definition)
			stub.onInvoke = func(input adapters.ReservedInvokeInput) {
				page, err := service.ListHistory(t.Context(), automations.ListHistoryParams{AutomationID: record.ID})
				if err != nil || len(page.Items) != 1 {
					t.Errorf("reservation history: %#v %v", page, err)
					return
				}
				entry := historyEntry(t, service, record.ID, page.Items[0].ID).(automations.Run)
				execution, reached := executionForStep(entry, "inspect")
				function, isFunction := execution.Details.(automations.FunctionExecutionDetails)
				_, running := function.State.(automations.FunctionRunning)
				if !reached || !isFunction || function.InvocationID != input.InvocationID || !running {
					t.Errorf("call preceded durable reservation: %#v", entry.Executions)
				}
			}
			run := admitDelayRun(t, service, record)
			waitForRuns(t, service)
			got := historyEntry(t, service, record.ID, string(run.ID)).(automations.Run)
			function := requireExecutionForStep(t, got, "inspect").Details.(automations.FunctionExecutionDetails)
			if automations.RunStateStatus(got.State) != test.want || scripted.executionCount() != test.commands ||
				len(stub.inputs) != 1 {
				t.Fatalf("run=%#v commands=%d inputs=%#v", got, scripted.executionCount(), stub.inputs)
			}
			input := stub.inputs[0]
			if input.AdapterID != "test-functions" || input.FunctionKey != "inspect" ||
				string(input.Arguments) != `{"value":9007199254740993}` ||
				input.InvocationID != function.InvocationID {
				t.Fatalf("invocation payload changed: %#v", input)
			}
			if _, err := automations.EncodeStepExecutionDetails(function); err != nil {
				t.Fatalf("invalid retained execution: %#v %v", function, err)
			}
		})
	}
}

func TestFunctionExecutionOnlySelectedLeavesReserveInvocations(t *testing.T) {
	t.Parallel()
	stub := &scriptedFunctions{outcome: adapters.Succeeded{Result: json.RawMessage(`{}`)}}
	deps := runtimeTestDependencies()
	deps.Functions = stub
	service, _ := newRuntimeService(t, newScriptedDevices(), deps)
	definition := runtimeDefinition(t, 0)
	definition.Steps = []automations.Step{functionNode(t, "top"), {ID: "branch", Body: automations.IfStep{
		Conditions: automations.Condition{
			ID:   "cause",
			Body: automations.TriggerCondition{TriggerIDs: []automations.TriggerID{"trigger"}},
		},
		Then: []automations.Step{functionNode(t, "unselected")},
		Else: []automations.Step{functionNode(t, "fallback")},
	}}}
	record := createRuntimeAutomation(t, service, definition)
	run := admitDelayRun(t, service, record)
	waitForRuns(t, service)
	got := historyEntry(t, service, record.ID, string(run.ID)).(automations.Run)
	if automations.RunStateStatus(got.State) != automations.RunSucceeded ||
		len(stub.inputs) != 2 || len(branchDecisions(got)) != 1 {
		t.Fatalf("selected Function execution = %#v", got)
	}
	for position, id := range []automations.StepID{"top", "fallback"} {
		function := requireExecutionForStep(t, got, id).Details.(automations.FunctionExecutionDetails)
		if function.InvocationID != stub.inputs[position].InvocationID {
			t.Fatalf("Function reach order = %#v", got.Executions)
		}
	}
}

func TestFunctionExecutionMatchesInvocationAcrossBackwardClockCorrections(t *testing.T) {
	t.Parallel()
	stub := &scriptedFunctions{outcome: adapters.Succeeded{Result: json.RawMessage(`{"ok":true}`)}}
	stub.changeRead = func(invocation *adapters.Invocation) {
		invocation.StartedAt = runtimeTestNow.Add(-time.Second)
		invocation.Deadline = invocation.StartedAt.Add(time.Second)
		invocation.Completion = &adapters.Completion{
			At: runtimeTestNow.Add(-2 * time.Second), Evidence: stub.outcome,
		}
	}
	deps := runtimeTestDependencies()
	deps.Functions = stub
	scripted := newScriptedDevices()
	service, _ := newRuntimeService(t, scripted, deps)
	definition := runtimeDefinition(t, 1)
	definition.Steps = append([]automations.Step{functionNode(t, "inspect")}, definition.Steps...)
	record := createRuntimeAutomation(t, service, definition)
	run := admitDelayRun(t, service, record)
	waitForRuns(t, service)
	got := historyEntry(t, service, record.ID, string(run.ID)).(automations.Run)
	if automations.RunStateStatus(got.State) != automations.RunSucceeded || len(stub.inputs) != 1 ||
		scripted.executionCount() != 1 {
		t.Fatalf(
			"backward clock blocked matching evidence: Run=%#v calls=%d commands=%d",
			got,
			len(stub.inputs),
			scripted.executionCount(),
		)
	}
	function := requireExecutionForStep(t, got, "inspect").Details.(automations.FunctionExecutionDetails)
	state, ok := function.State.(automations.FunctionSucceeded)
	if !ok || !state.CompletedAt.Equal(runtimeTestNow.Add(-2*time.Second)) {
		t.Fatalf("completion timestamp changed: %#v", function)
	}
}

func TestFunctionMixedSequenceCommitsEachKindBeforeAdvancing(t *testing.T) {
	t.Parallel()
	var trace []string
	notificationCount := 0
	var service *automations.Service
	var record automations.Record
	fixture := newNotificationFixture(t, func(_ context.Context, attempt notifications.Attempt) notifications.Evidence {
		trace = append(trace, "notification")
		notificationCount++
		if attempt.Message != (notifications.Message{Title: "Sequence", Text: "After Function and delay"}) {
			t.Errorf("notification payload changed: %#v", attempt.Message)
		}
		page, err := service.ListHistory(t.Context(), automations.ListHistoryParams{AutomationID: record.ID})
		if err != nil || len(page.Items) != 1 {
			t.Errorf("reached history: %#v %v", page, err)
			return notifications.Evidence{Outcome: notifications.Rejected, ReasonCode: "provider_rejected"}
		}
		entry, err := service.GetHistoryEntry(t.Context(), record.ID, page.Items[0].ID)
		if err != nil {
			t.Error(err)
			return notifications.Evidence{Outcome: notifications.Rejected, ReasonCode: "provider_rejected"}
		}
		run := entry.(automations.Run)
		functionID, delayID := automations.StepID("inspect"), automations.StepID("wait")
		if notificationCount == 2 {
			functionID, delayID = "inspect_again", "wait_again"
		}
		function := requireExecutionForStep(t, run, functionID).Details.(automations.FunctionExecutionDetails)
		delay := requireExecutionForStep(t, run, delayID).Details.(automations.DelayExecutionDetails)
		if _, succeeded := function.State.(automations.FunctionSucceeded); !succeeded {
			t.Errorf("notification preceded retained Function/delay success: %#v", run)
		}
		if _, succeeded := delay.State.(automations.DelaySucceeded); !succeeded {
			t.Errorf("notification preceded retained Function/delay success: %#v", run)
		}
		return notifications.Evidence{Outcome: notifications.Accepted, Receipt: "sequence-receipt"}
	})
	stub := &scriptedFunctions{outcome: adapters.Succeeded{Result: json.RawMessage(`{"ok":true}`)}}
	stub.onInvoke = func(adapters.ReservedInvokeInput) { trace = append(trace, "function") }
	deps := runtimeTestDependencies()
	deps.Functions, deps.Notifications = stub, fixture.service
	scripted := newScriptedDevices()
	scripted.onStart = func(input devices.CommandInput) { trace = append(trace, string(input.EntityID)) }
	service = automations.NewService(automationssqlite.NewAutomationRepository(fixture.database, deps), scripted, deps)
	definition := runtimeDefinition(t, 2)
	first, last := definition.Steps[0], definition.Steps[1]
	notify := automations.Step{ID: "notify", Body: automations.NotificationStep{
		DestinationID: fixture.destination.ID,
		Title:         "Sequence",
		Message:       "After Function and delay",
	}}
	notifyAgain := notify
	notifyAgain.ID = "notify_again"
	definition.Steps = []automations.Step{
		first,
		functionNode(t, "inspect"),
		branchIf("outer", branchTriggerCondition(true), []automations.Step{
			{ID: "wait", Body: automations.DelayStep{DurationMS: 1}},
			branchIf("nested", branchTriggerCondition(true), []automations.Step{
				notify,
				functionNode(t, "inspect_again"),
				{ID: "wait_again", Body: automations.DelayStep{DurationMS: 1}},
			}, nil),
			notifyAgain,
		}, nil),
		last,
	}
	record = createRuntimeAutomation(t, service, definition)
	run := admitDelayRun(t, service, record)
	waitForRuns(t, service)
	retained := historyEntry(t, service, record.ID, string(run.ID)).(automations.Run)
	want := []string{
		string(first.Body.(automations.CommandStep).EntityID),
		"function",
		"notification",
		"function",
		"notification",
		string(last.Body.(automations.CommandStep).EntityID),
	}
	if !reflect.DeepEqual(trace, want) || automations.RunStateStatus(retained.State) != automations.RunSucceeded ||
		len(retained.Executions) != 10 || scripted.executionCount() != 2 || len(stub.inputs) != 2 {
		t.Fatalf("mixed sequence: trace=%v want=%v Run=%#v", trace, want, retained)
	}
	wantSequence := []automations.StepID{
		first.ID,
		"inspect",
		"outer",
		"wait",
		"nested",
		"notify",
		"inspect_again",
		"wait_again",
		"notify_again",
		last.ID,
	}
	for sequence, execution := range retained.Executions {
		if execution.Sequence != sequence || execution.StepID != wantSequence[sequence] {
			t.Fatalf("global execution order = %#v, want %v", retained.Executions, wantSequence)
		}
	}
}

func TestFunctionExecutionRejectsUnverifiableAndPreAdmissionEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		configure func(*scriptedFunctions)
		status    automations.RunStatus
	}{
		{"missing Function", func(s *scriptedFunctions) { s.invokeErr = adapters.ErrFunctionNotFound }, automations.RunFailed},
		{"contract changed", func(s *scriptedFunctions) { s.invokeErr = adapters.ErrFunctionContractChanged }, automations.RunFailed},
		{"offline", func(s *scriptedFunctions) { s.invokeErr = adapters.ErrAdapterUnavailable }, automations.RunFailed},
		{"catalog changed", func(s *scriptedFunctions) { s.invokeErr = adapters.ErrCatalogChanged }, automations.RunFailed},
		{"admission closed", func(s *scriptedFunctions) { s.invokeErr = adapters.ErrFunctionAdmissionClosed }, automations.RunFailed},
		{"persistence", func(s *scriptedFunctions) { s.invokeErr = adapters.ErrFunctionPersistence }, automations.RunInterrupted},
		{"identity conflict", func(s *scriptedFunctions) { s.invokeErr = adapters.ErrInvocationIDConflict }, automations.RunInterrupted},
		{"read failure", func(s *scriptedFunctions) { s.readErr = errors.New("read failed") }, automations.RunInterrupted},
		{"missing", func(s *scriptedFunctions) { s.readErr = adapters.ErrInvocationNotFound }, automations.RunInterrupted},
		{"ID", func(s *scriptedFunctions) {
			s.changeRead = func(i *adapters.Invocation) { i.ID = "afi_01980000-0000-7000-8000-000000000099" }
		}, automations.RunInterrupted},
		{"Adapter", func(s *scriptedFunctions) {
			s.changeRead = func(i *adapters.Invocation) { i.Route.AdapterID = "other" }
		}, automations.RunInterrupted},
		{"arguments", func(s *scriptedFunctions) {
			s.changeRead = func(i *adapters.Invocation) { i.Arguments = json.RawMessage(`{"value":1}`) }
		}, automations.RunInterrupted},
		{"contract", func(s *scriptedFunctions) { s.changeRead = func(i *adapters.Invocation) { i.Function.TimeoutMS++ } }, automations.RunInterrupted},
		{"nonterminal", func(s *scriptedFunctions) { s.changeRead = func(i *adapters.Invocation) { i.Completion = nil } }, automations.RunInterrupted},
		{"invalid result", func(s *scriptedFunctions) {
			s.changeRead = func(i *adapters.Invocation) {
				c := *i.Completion
				c.Evidence = adapters.Succeeded{Result: json.RawMessage(`[]`)}
				i.Completion = &c
			}
		}, automations.RunInterrupted},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stub := &scriptedFunctions{outcome: adapters.Succeeded{Result: json.RawMessage(`{}`)}}
			test.configure(stub)
			deps := runtimeTestDependencies()
			deps.Functions = stub
			scripted := newScriptedDevices()
			service, _ := newRuntimeService(t, scripted, deps)
			definition := runtimeDefinition(t, 1)
			definition.Steps = append([]automations.Step{functionNode(t, "inspect")}, definition.Steps...)
			record := createRuntimeAutomation(t, service, definition)
			run := admitDelayRun(t, service, record)
			waitForRuns(t, service)
			got := historyEntry(t, service, record.ID, string(run.ID)).(automations.Run)
			if automations.RunStateStatus(got.State) != test.status || len(stub.inputs) != 1 ||
				scripted.executionCount() != 0 ||
				!service.AdmissionOpen() {
				t.Fatalf("run=%#v calls=%d commands=%d", got, len(stub.inputs), scripted.executionCount())
			}
		})
	}
}

type functionFaultRepository struct {
	automations.Repository

	boundary string
}

func (repo *functionFaultRepository) RecordStep(ctx context.Context, record automations.StepExecutionRecord) error {
	if _, isFunction := record.Evidence.(automations.FunctionStarted); !isFunction {
		return repo.Repository.RecordStep(ctx, record)
	}
	if repo.boundary == "function_start" {
		return errors.New("Function start write failed")
	}
	return repo.Repository.RecordStep(ctx, record)
}

func (repo *functionFaultRepository) CompleteStep(
	ctx context.Context,
	completion automations.StepExecutionCompletion,
) error {
	if _, isFunction := completion.Evidence.(automations.FunctionFinished); !isFunction {
		return repo.Repository.CompleteStep(ctx, completion)
	}
	if repo.boundary == "function_complete" {
		return errors.New("Function completion write failed")
	}
	return repo.Repository.CompleteStep(ctx, completion)
}

func TestFunctionPersistenceFaultDoesNotDispatchOrAdvance(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"function_start", "function_complete"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			stub := &scriptedFunctions{outcome: adapters.Succeeded{Result: json.RawMessage(`{}`)}}
			deps := runtimeTestDependencies()
			deps.Functions = stub
			repo := &functionFaultRepository{
				Repository: automationssqlite.NewAutomationRepository(openAutomationDatabase(t), deps),
				boundary:   boundary,
			}
			scripted := newScriptedDevices()
			service := automations.NewService(repo, scripted, deps)
			definition := runtimeDefinition(t, 1)
			definition.Steps = append([]automations.Step{functionNode(t, "inspect")}, definition.Steps...)
			record := createRuntimeAutomation(t, service, definition)
			run := admitDelayRun(t, service, record)
			waitForRuns(t, service)
			got := historyEntry(t, service, record.ID, string(run.ID)).(automations.Run)
			calls := 0
			if boundary == "function_complete" {
				calls = 1
			}
			if len(stub.inputs) != calls || scripted.executionCount() != 0 ||
				automations.RunStateStatus(got.State) != automations.RunInterrupted ||
				!service.AdmissionOpen() {
				t.Fatalf("fault allowed progress: %#v inputs=%#v", got, stub.inputs)
			}
		})
	}
}

func TestFunctionShutdownBetweenGateAndReservationDoesNotDispatch(t *testing.T) {
	t.Parallel()
	stub := &scriptedFunctions{outcome: adapters.Succeeded{Result: json.RawMessage(`{}`)}}
	deps := runtimeTestDependencies()
	deps.Functions = stub
	var service *automations.Service
	deps.NewInvocationID = func() (adapters.InvocationID, error) {
		service.StopAdmission()
		return adapters.NewInvocationID()
	}
	scripted := newScriptedDevices()
	service, _ = newRuntimeService(t, scripted, deps)
	definition := runtimeDefinition(t, 1)
	definition.Steps = append([]automations.Step{functionNode(t, "inspect")}, definition.Steps...)
	record := createRuntimeAutomation(t, service, definition)
	run := admitDelayRun(t, service, record)
	waitForRuns(t, service)
	got := historyEntry(t, service, record.ID, string(run.ID)).(automations.Run)
	completed, ok := got.State.(automations.CompletedRun)
	interrupted, interruptedOK := completed.Outcome.(automations.InterruptedRun)
	if !ok || !interruptedOK || interrupted.FailureCode != automations.FailureCoreStopping ||
		len(got.Executions) != 0 || len(stub.inputs) != 0 || scripted.executionCount() != 0 {
		t.Fatalf("shutdown reserved or dispatched new work: Run=%+v inputs=%+v", got, stub.inputs)
	}
}
