//go:build integration

package automations_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// The next Step must wait for the prior Command's successful terminal outcome.
func TestRunExecutesStepsSequentially(t *testing.T) {
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

	run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if scripted.executionCount() != 1 {
		t.Fatalf("executions before Step 1 completed = %d, want 1", scripted.executionCount())
	}
	midway := historyEntry(t, service, record.ID, string(run.ID))
	if runEntry(midway) == nil {
		t.Fatalf("midway entry is not a Run: %#v", midway)
	}
	leaves := automations.CommandLeaves(runEntry(midway).Snapshot.Steps)
	first := requireExecutionForStep(t, *runEntry(midway), leaves[0].ID).Body.(automations.CommandExecution)
	if first.CompletedAt != nil || first.Reservation == nil || first.Outcome != nil {
		t.Fatalf("first command execution = %#v, want running reservation", first)
	}
	if _, reached := executionForStep(*runEntry(midway), leaves[1].ID); reached {
		t.Fatalf("second command reached before first completed: %#v", runEntry(midway).Executions)
	}
	close(gate)
	waitForRuns(t, service)

	finished := historyEntry(t, service, record.ID, string(run.ID))
	if runEntry(finished) == nil || automations.RunStateStatus(runEntry(finished).State) != automations.RunSucceeded {
		t.Fatalf("finished Run = %#v", runEntry(finished))
	}
	if scripted.executionCount() != 2 {
		t.Fatalf("executions after success = %d, want 2", scripted.executionCount())
	}
	for _, leaf := range automations.CommandLeaves(runEntry(finished).Snapshot.Steps) {
		command := requireExecutionForStep(t, *runEntry(finished), leaf.ID).Body.(automations.CommandExecution)
		if automations.StepOutcomeStatus(command.Outcome) != automations.StepSatisfied ||
			func() bool { verified, _ := automations.StepOutcomeEvidence(command.Outcome); return verified == nil }() {
			t.Fatalf("command execution %q = %#v, want verified success", leaf.ID, command)
		}
	}
}

// The first failure stops the Run without retries; later Steps remain absent from Executions.
func TestRunStopsAtFirstFailureWithoutRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	failureCode := devices.CommandFailureUpstreamRejected
	scripted.execute = func(_ context.Context, input devices.CommandInput) (devices.CommandResult, error) {
		scripted.recordCommand(terminalCommand(t, input, devices.CommandStatusRejected, &failureCode))
		return devices.CommandResult{}, &devices.CommandExecutionError{
			CommandID: input.ID, Err: devices.ErrUpstreamRejected,
		}
	}
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 3))

	run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

	if scripted.executionCount() != 1 {
		t.Fatalf("executions = %d, want exactly 1 (no retry, no later Step)", scripted.executionCount())
	}
	entry := historyEntry(t, service, record.ID, string(run.ID))
	if runEntry(entry) == nil || automations.RunStateStatus(runEntry(entry).State) != automations.RunFailed {
		t.Fatalf("Run = %#v, want failed", runEntry(entry))
	}
	if runFailure(runEntry(entry).State) == nil || *runFailure(runEntry(entry).State) != string(failureCode) {
		t.Fatalf("Run failure code = %v, want %q", runFailure(runEntry(entry).State), failureCode)
	}
	leaves := automations.CommandLeaves(runEntry(entry).Snapshot.Steps)
	first := requireExecutionForStep(t, *runEntry(entry), leaves[0].ID).Body.(automations.CommandExecution)
	if automations.StepOutcomeStatus(first.Outcome) != automations.StepFailed {
		t.Fatalf("first command = %#v, want failed", first)
	}
	for _, leaf := range leaves[1:] {
		if execution, reached := executionForStep(*runEntry(entry), leaf.ID); reached {
			t.Fatalf("later command was reached: %#v", execution)
		}
	}
}

// A mismatched Command must stop only its Run with executor_fault without
// exposing an unverified link.
func TestRunLinksCommandOnlyAfterOwnershipVerification(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	otherCorrelation, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	scripted.execute = func(_ context.Context, input devices.CommandInput) (devices.CommandResult, error) {
		record := terminalCommand(t, input, devices.CommandStatusSatisfied, nil)
		record.CorrelationID = otherCorrelation
		scripted.recordCommand(record)
		return devices.CommandResult{CommandID: input.ID, Outcome: devices.OutcomeDispatched}, nil
	}
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))

	run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

	entry := historyEntry(t, service, record.ID, string(run.ID))
	if runEntry(entry) == nil || automations.RunStateStatus(runEntry(entry).State) != automations.RunInterrupted {
		t.Fatalf("Run = %#v, want interrupted", runEntry(entry))
	}
	if runFailure(runEntry(entry).State) == nil ||
		*runFailure(runEntry(entry).State) != automations.FailureExecutorFault {
		t.Fatalf("Run failure code = %v, want executor_fault", runFailure(runEntry(entry).State))
	}
	leaf := automations.CommandLeaves(runEntry(entry).Snapshot.Steps)[0]
	command := requireExecutionForStep(t, *runEntry(entry), leaf.ID).Body.(automations.CommandExecution)
	verified, _ := automations.StepOutcomeEvidence(command.Outcome)
	if automations.StepOutcomeStatus(command.Outcome) != automations.StepInterrupted || verified != nil {
		t.Fatalf("command execution = %#v, want interrupted without a verified link", command)
	}
	if !service.AdmissionOpen() {
		t.Fatal("executor fault closed automation admission")
	}
	if _, err = service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID}); err != nil {
		t.Fatalf("independent post-fault Run admission = %v", err)
	}
	waitForRuns(t, service)
}

// CommandExecutionError proves creation; missing or nonterminal durable evidence
// must therefore fault rather than invent an outcome.
func TestRunTreatsMissingOrNonterminalCommandAsFault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, test := range []struct {
		name    string
		execute func(*scriptedDevices, devices.CommandInput) (devices.CommandResult, error)
	}{
		{
			"missing command",
			func(_ *scriptedDevices, input devices.CommandInput) (devices.CommandResult, error) {
				return devices.CommandResult{}, &devices.CommandExecutionError{
					CommandID: input.ID, Err: errors.New("dispatch failed"),
				}
			},
		},
		{
			"nonterminal command",
			func(scripted *scriptedDevices, input devices.CommandInput) (devices.CommandResult, error) {
				record := terminalCommand(t, input, devices.CommandStatusSatisfied, nil)
				record.CompletedAt = nil
				scripted.recordCommand(record)
				return devices.CommandResult{}, &devices.CommandExecutionError{
					CommandID: input.ID, Err: errors.New("dispatch failed"),
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			scripted := newScriptedDevices()
			scripted.execute = func(_ context.Context, input devices.CommandInput) (devices.CommandResult, error) {
				return test.execute(scripted, input)
			}
			service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
			record := createRuntimeAutomation(t, service, runtimeDefinition(t, 2))

			run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID})
			if err != nil {
				t.Fatal(err)
			}
			waitForRuns(t, service)

			entry := historyEntry(t, service, record.ID, string(run.ID))
			if runEntry(entry) == nil ||
				automations.RunStateStatus(runEntry(entry).State) != automations.RunInterrupted ||
				runFailure(runEntry(entry).State) == nil ||
				*runFailure(runEntry(entry).State) != automations.FailureExecutorFault {
				t.Fatalf("Run = %#v, want interrupted/executor_fault", runEntry(entry))
			}
			leaves := automations.CommandLeaves(runEntry(entry).Snapshot.Steps)
			if _, reached := executionForStep(*runEntry(entry), leaves[1].ID); reached {
				t.Fatalf("later command was reached: %#v", runEntry(entry).Executions)
			}
			if !service.AdmissionOpen() {
				t.Fatal("executor fault closed admission")
			}
		})
	}
}

// Confirmed pre-creation rejection fails the Step without an executor fault or Command link.
func TestRunClassifiesPreCreationFailureWithoutFault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	scripted.execute = func(context.Context, devices.CommandInput) (devices.CommandResult, error) {
		return devices.CommandResult{}, devices.ErrInvalidCommand
	}
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 2))

	run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

	entry := historyEntry(t, service, record.ID, string(run.ID))
	if runEntry(entry) == nil || automations.RunStateStatus(runEntry(entry).State) != automations.RunFailed {
		t.Fatalf("Run = %#v, want failed", runEntry(entry))
	}
	leaf := automations.CommandLeaves(runEntry(entry).Snapshot.Steps)[0]
	command := requireExecutionForStep(t, *runEntry(entry), leaf.ID).Body.(automations.CommandExecution)
	_, failure := automations.StepOutcomeEvidence(command.Outcome)
	if automations.StepOutcomeStatus(command.Outcome) != automations.StepFailed || failure == nil ||
		*failure != automations.FailureInvalidCommand {
		t.Fatalf("command execution = %#v, want invalid_command failure", command)
	}
	if !service.AdmissionOpen() {
		t.Fatal("pre-creation failure latched a fault")
	}
}
