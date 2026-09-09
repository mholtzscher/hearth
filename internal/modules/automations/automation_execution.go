package automations

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const automationPersistenceTimeout = 5 * time.Second
const automationPruneBatchSize = 500

// StopAutomationExecutionAdmission atomically closes Run and next-Step admission.
// Commands whose intent committed before closure still execute and drain.
func (service *Service) StopAutomationExecutionAdmission() {
	service.gate.Lock()
	defer service.gate.Unlock()
	service.admissionOpen = false
}

// AutomationExecutionReady is false during shutdown or after a latched executor fault.
// App readiness must combine this with device readiness (and spec 2 scheduling).
func (service *Service) AutomationExecutionReady() bool {
	service.gate.Lock()
	defer service.gate.Unlock()
	return service.admissionOpen && !service.executorFault
}

// WaitAutomationRuns joins registered workers without canceling current Commands.
// Close admission first to guarantee no worker can be registered after this wait.
// Fault-retained active database claims deliberately outlive their workers.
func (service *Service) WaitAutomationRuns(ctx context.Context) error {
	service.gate.Lock()
	idle := service.idle
	service.gate.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (service *Service) latchAutomationFaultLocked() {
	if service.executorFault {
		return
	}
	service.executorFault = true
	service.admissionOpen = false
	service.logger.Error("automation execution fault latched until restart",
		slog.String("event", "automation.execution_fault"), slog.String("error_code", "automation_unavailable"))
}

func (service *Service) latchAutomationFault() {
	service.gate.Lock()
	defer service.gate.Unlock()
	service.latchAutomationFaultLocked()
}

func (service *Service) executeAutomationRun(runID AutomationRunID, definition AutomationDefinition) {
	defer func() {
		service.gate.Lock()
		defer service.gate.Unlock()
		service.workers--
		if service.workers == 0 {
			close(service.idle)
		}
	}()
	for index, stepDefinition := range definition.Steps {
		step, admitted := service.beginAutomationStep(runID, index, stepDefinition)
		if !admitted {
			return
		}
		// Process-owned, not request- or shutdown-cancelled. Devices own the deadline.
		_, executionErr := service.commands.ExecuteCommand(context.Background(), devices.CommandInput{
			ID:            *step.ReservedCommandID,
			CorrelationID: *step.ReservedCorrelationID,
			EntityID:      stepDefinition.EntityID,
			OperationName: stepDefinition.OperationName,
			Parameters:    stepDefinition.Parameters,
		})
		completion, established := service.reconcileAutomationStep(step, executionErr)
		if !established {
			service.latchAutomationFault()
			return
		}
		completion.RunID = runID
		completion.Index = index
		if !service.persistAutomationStep(completion) {
			return
		}
		if completion.Status == AutomationStepStatusFailed {
			return
		}
	}
	service.finishAutomationRun(AutomationRunCompletion{RunID: runID, Status: AutomationRunStatusSucceeded})
}

// beginAutomationStep uses the same gate as Run admission and shutdown. The
// registered Run worker owns every committed intent, even before ExecuteCommand.
func (service *Service) beginAutomationStep(
	runID AutomationRunID,
	index int,
	definition AutomationStep,
) (AutomationRunStep, bool) {
	service.gate.Lock()
	defer service.gate.Unlock()
	if !service.admissionOpen {
		// Only known sequences reach next-Step admission: uncertain workers
		// return at the fault site. A different Run's fault must not retain this
		// claim after its current Command and Step result have drained.
		code := AutomationFailureCoreStopping
		service.finishAutomationRunLocked(
			AutomationRunCompletion{RunID: runID, Status: AutomationRunStatusInterrupted, FailureCode: &code},
		)
		return AutomationRunStep{}, false
	}
	commandID, err := service.newCommandID()
	if err != nil {
		service.latchAutomationFaultLocked()
		return AutomationRunStep{}, false
	}
	correlationID, err := service.newCorrelationID()
	if err != nil {
		service.latchAutomationFaultLocked()
		return AutomationRunStep{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), automationPersistenceTimeout)
	defer cancel()
	if err = service.repo.BeginAutomationStep(
		ctx,
		AutomationStepStart{RunID: runID, Index: index, CommandID: commandID, CorrelationID: correlationID},
	); err != nil {
		service.latchAutomationFaultLocked()
		return AutomationRunStep{}, false
	}
	return AutomationRunStep{Index: index, Definition: definition, Status: AutomationStepStatusRunning,
		ReservedCommandID: &commandID, ReservedCorrelationID: &correlationID}, true
}

func (service *Service) finishAutomationRun(input AutomationRunCompletion) {
	service.gate.Lock()
	defer service.gate.Unlock()
	service.finishAutomationRunLocked(input)
}

func (service *Service) finishAutomationRunLocked(input AutomationRunCompletion) {
	ctx, cancel := context.WithTimeout(context.Background(), automationPersistenceTimeout)
	defer cancel()
	if err := service.repo.CompleteAutomationRun(ctx, input); err != nil {
		service.latchAutomationFaultLocked()
	}
}

// reconcileAutomationStep never treats CommandExecutionError as proof of a
// terminal write. Even successful returns are checked against durable ownership.
func (service *Service) reconcileAutomationStep(
	step AutomationRunStep,
	executionErr error,
) (AutomationStepCompletion, bool) {
	if errors.Is(executionErr, devices.ErrCommandIDConflict) {
		return failedAutomationStep(AutomationFailureCommandIDConflict), true
	}
	if _, created := errors.AsType[*devices.CommandExecutionError](executionErr); !created {
		switch {
		case errors.Is(executionErr, devices.ErrInvalidCommand):
			return failedAutomationStep(AutomationFailureInvalidCommand), true
		case errors.Is(executionErr, devices.ErrEntityNotFound):
			return failedAutomationStep(AutomationFailureEntityNotFound), true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), automationPersistenceTimeout)
	defer cancel()
	record, err := service.commandRecords.GetCommand(ctx, *step.ReservedCommandID)
	if errors.Is(err, devices.ErrCommandNotFound) && executionErr != nil {
		return failedAutomationStep(AutomationFailureInternalError), true
	}
	if err != nil || !AutomationStepOwnsCommand(step, record) || record.CompletedAt == nil {
		return AutomationStepCompletion{}, false
	}
	switch record.Status {
	case devices.CommandStatusRequested, devices.CommandStatusAccepted:
		return AutomationStepCompletion{}, false
	case devices.CommandStatusSatisfied:
		outcome := devices.OutcomeObserved
		return AutomationStepCompletion{Status: AutomationStepStatusSatisfied, Outcome: &outcome}, true
	case devices.CommandStatusDispatched:
		outcome := devices.OutcomeDispatched
		return AutomationStepCompletion{Status: AutomationStepStatusDispatched, Outcome: &outcome}, true
	case devices.CommandStatusRejected, devices.CommandStatusAdapterUnhealthy,
		devices.CommandStatusEntityUnavailable, devices.CommandStatusOutcomeTimeout,
		devices.CommandStatusEntityDisabled, devices.CommandStatusInternalFailure, devices.CommandStatusInterrupted:
		code := AutomationFailureInternalError
		if record.FailureCode != nil {
			code = string(*record.FailureCode)
		}
		return failedAutomationStep(code), true
	default:
		return AutomationStepCompletion{}, false
	}
}

func failedAutomationStep(code string) AutomationStepCompletion {
	return AutomationStepCompletion{Status: AutomationStepStatusFailed, FailureCode: &code}
}

// PruneAutomationHistory sweeps eligible terminal history in bounded transactions.
// App calls it hourly, never at startup. Active claims are never pruned.
func (service *Service) PruneAutomationHistory(ctx context.Context, cutoff time.Time) error {
	for {
		count, err := service.repo.PruneAutomationHistory(ctx, cutoff)
		if err != nil {
			return err
		}
		if count < automationPruneBatchSize {
			return nil
		}
	}
}

// persistAutomationStep serializes result failure detection with next-Step admission.
func (service *Service) persistAutomationStep(completion AutomationStepCompletion) bool {
	service.gate.Lock()
	defer service.gate.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), automationPersistenceTimeout)
	defer cancel()
	if err := service.repo.CompleteAutomationStep(ctx, completion); err != nil {
		service.latchAutomationFaultLocked()
		return false
	}
	return true
}
