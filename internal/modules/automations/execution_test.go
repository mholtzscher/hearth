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

func terminalCommand(
	t *testing.T,
	input devices.CommandInput,
	status devices.CommandStatus,
	failureCode *devices.CommandFailureCode,
) devices.CommandRecord {
	t.Helper()
	completedAt := time.Now().UTC()
	return devices.CommandRecord{
		ID:            input.ID,
		CorrelationID: input.CorrelationID,
		EntityID:      input.EntityID,
		OperationName: input.OperationName,
		Parameters:    append(devices.CommandParameters(nil), input.Parameters...),
		Status:        status,
		CompletedAt:   &completedAt,
		FailureCode:   failureCode,
	}
}

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
	if midway.Run == nil {
		t.Fatalf("midway entry is not a Run: %#v", midway)
	}
	if midway.Run.Steps[0].Status != automations.StepRunning {
		t.Fatalf("Step 1 status = %q, want running", midway.Run.Steps[0].Status)
	}
	if midway.Run.Steps[1].Status != automations.StepNotAttempted {
		t.Fatalf("Step 2 status = %q, want not_attempted", midway.Run.Steps[1].Status)
	}
	close(gate)
	waitForRuns(t, service)

	finished := historyEntry(t, service, record.ID, string(run.ID))
	if finished.Run == nil || finished.Run.Status != automations.RunSucceeded {
		t.Fatalf("finished Run = %#v", finished.Run)
	}
	if scripted.executionCount() != 2 {
		t.Fatalf("executions after success = %d, want 2", scripted.executionCount())
	}
	for position, step := range finished.Run.Steps {
		if step.Status != automations.StepSatisfied || step.VerifiedCommandID == nil {
			t.Fatalf("Step %d = %#v, want satisfied with a verified Command", position, step)
		}
	}
}

// The first failure stops the Run without retries; later Steps stay not_attempted.
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
	if entry.Run == nil || entry.Run.Status != automations.RunFailed {
		t.Fatalf("Run = %#v, want failed", entry.Run)
	}
	if entry.Run.FailureCode == nil || *entry.Run.FailureCode != string(failureCode) {
		t.Fatalf("Run failure code = %v, want %q", entry.Run.FailureCode, failureCode)
	}
	if entry.Run.Steps[0].Status != automations.StepFailed {
		t.Fatalf("Step 1 = %#v, want failed", entry.Run.Steps[0])
	}
	for position, step := range entry.Run.Steps[1:] {
		if step.Status != automations.StepNotAttempted {
			t.Fatalf("Step %d = %#v, want not_attempted", position+1, step)
		}
	}
}

// A mismatched Command must latch executor_fault and close admission without
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
	if entry.Run == nil || entry.Run.Status != automations.RunInterrupted {
		t.Fatalf("Run = %#v, want interrupted", entry.Run)
	}
	if entry.Run.FailureCode == nil || *entry.Run.FailureCode != automations.FailureExecutorFault {
		t.Fatalf("Run failure code = %v, want executor_fault", entry.Run.FailureCode)
	}
	if entry.Run.Steps[0].Status != automations.StepInterrupted ||
		entry.Run.Steps[0].VerifiedCommandID != nil {
		t.Fatalf("Step = %#v, want interrupted without a verified link", entry.Run.Steps[0])
	}
	if service.AdmissionOpen() {
		t.Fatal("executor fault left automation admission open")
	}
	if _, err = service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID}); !errors.Is(
		err, automations.ErrAdmissionUnavailable,
	) {
		t.Fatalf("post-fault start error = %v, want ErrAdmissionUnavailable", err)
	}
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
			if entry.Run == nil || entry.Run.Status != automations.RunInterrupted ||
				entry.Run.FailureCode == nil ||
				*entry.Run.FailureCode != automations.FailureExecutorFault {
				t.Fatalf("Run = %#v, want interrupted/executor_fault", entry.Run)
			}
			if entry.Run.Steps[1].Status != automations.StepNotAttempted {
				t.Fatalf("Step 2 = %#v, want not_attempted", entry.Run.Steps[1])
			}
			if service.AdmissionOpen() {
				t.Fatal("executor fault left admission open")
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
	if entry.Run == nil || entry.Run.Status != automations.RunFailed {
		t.Fatalf("Run = %#v, want failed", entry.Run)
	}
	if entry.Run.Steps[0].Status != automations.StepFailed ||
		entry.Run.Steps[0].FailureCode == nil ||
		*entry.Run.Steps[0].FailureCode != automations.FailureInvalidCommand {
		t.Fatalf("Step 1 = %#v, want invalid_command failure", entry.Run.Steps[0])
	}
	if !service.AdmissionOpen() {
		t.Fatal("pre-creation failure latched a fault")
	}
}
