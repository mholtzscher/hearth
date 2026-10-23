//go:build integration

package automations_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
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
				if len(entry.Functions) != 1 || entry.Functions[0].InvocationID != input.InvocationID ||
					automations.FunctionStateStatus(entry.Functions[0].State) != "running" {
					t.Errorf("call preceded durable reservation: %#v", entry.Functions)
				}
			}
			run := admitDelayRun(t, service, record)
			waitForRuns(t, service)
			got := historyEntry(t, service, record.ID, string(run.ID)).(automations.Run)
			if automations.RunStateStatus(got.State) != test.want || scripted.executionCount() != test.commands ||
				len(stub.inputs) != 1 ||
				len(got.Functions) != 1 {
				t.Fatalf("run=%#v commands=%d inputs=%#v", got, scripted.executionCount(), stub.inputs)
			}
			input := stub.inputs[0]
			if input.AdapterID != "test-functions" || input.FunctionKey != "inspect" ||
				string(input.Arguments) != `{"value":9007199254740993}` ||
				input.InvocationID != got.Functions[0].InvocationID {
				t.Fatalf("invocation payload changed: %#v", input)
			}
			raw, err := automations.EncodeFunctionExecution(got.Functions[0])
			if err != nil {
				t.Fatalf("invalid retained evidence: %s %v", raw, err)
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
	if automations.RunStateStatus(got.State) != automations.RunSucceeded || len(got.Functions) != 2 ||
		len(stub.inputs) != 2 || len(got.Steps) != 0 || len(got.BranchDecisions) != 1 {
		t.Fatalf("selected Function execution = %#v", got)
	}
	for position, id := range []automations.StepID{"top", "fallback"} {
		if got.Functions[position].StepID != id || got.Functions[position].Position != position ||
			got.Functions[position].InvocationID != stub.inputs[position].InvocationID {
			t.Fatalf("Function reach order = %#v", got.Functions)
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

func (repo *functionFaultRepository) RecordFunctionStart(ctx context.Context, start automations.FunctionStart) error {
	if repo.boundary == "start" {
		return errors.New("Function start write failed")
	}
	return repo.Repository.RecordFunctionStart(ctx, start)
}

func (repo *functionFaultRepository) CompleteFunction(
	ctx context.Context,
	completion automations.FunctionCompletion,
) error {
	if repo.boundary == "completion" {
		return errors.New("Function completion write failed")
	}
	return repo.Repository.CompleteFunction(ctx, completion)
}

func TestFunctionPersistenceFaultDoesNotDispatchOrAdvance(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"start", "completion"} {
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
			if boundary == "completion" {
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
