package automations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// automationPersistenceTimeout bounds one automation-owned SQLite read or write.
// It never wraps the external Command call, which devices owns and deadlines.
const automationPersistenceTimeout = 5 * time.Second

// Stable Step and Run failure codes. They are fixed tokens, never upstream text.
const (
	// AutomationFailureCoreStopping marks a Step or Run stopped because Command
	// admission closed during drain.
	AutomationFailureCoreStopping = "core_stopping"
	// AutomationFailureCoreRestarted marks a Step or Run interrupted by restart.
	AutomationFailureCoreRestarted = "core_restarted"
	// AutomationFailureExecutorFault marks a Step or Run the executor could not
	// advance truthfully, such as an unverifiable or nonterminal Command.
	AutomationFailureExecutorFault = "executor_fault"
	// AutomationFailureInvalidCommand marks a Step rejected before Command creation.
	AutomationFailureInvalidCommand = "invalid_command"
	// AutomationFailureEntityNotFound marks a Step whose Entity vanished before
	// Command creation.
	AutomationFailureEntityNotFound = "entity_not_found"
	// AutomationFailureInternalError is the fallback for one confirmed Command
	// failure with no more specific durable code.
	AutomationFailureInternalError = "internal_error"
)

// ValidateStepCompletion rejects a Step completion that is not a terminal
// outcome, or whose status does not carry the evidence it requires. Persistence
// calls it before writing; it performs no reads or writes of its own.
func ValidateStepCompletion(completion StepCompletion) error {
	switch completion.Status {
	case StepNotAttempted, StepRunning:
		return fmt.Errorf("%w: step completion status %q is not terminal", ErrInvalidAutomation, completion.Status)
	case StepSatisfied, StepDispatched:
		if completion.VerifiedCommandID == nil || completion.FailureCode != nil {
			return fmt.Errorf(
				"%w: successful step requires a verified Command and no failure code",
				ErrInvalidAutomation,
			)
		}
	case StepFailed, StepInterrupted:
		if completion.FailureCode == nil {
			return fmt.Errorf("%w: failing step requires a failure code", ErrInvalidAutomation)
		}
	default:
		return fmt.Errorf("%w: unknown step completion status %q", ErrInvalidAutomation, completion.Status)
	}
	if completion.VerifiedCommandID != nil {
		if _, err := devices.ParseCommandID(string(*completion.VerifiedCommandID)); err != nil {
			return fmt.Errorf("%w: verified command ID: %w", ErrInvalidAutomation, err)
		}
	}
	return nil
}

// executeRun executes one immutable Run snapshot sequentially. The next Step
// starts only after the prior Command reaches a successful terminal outcome, and
// the first failure or interruption stops the Run without retry.
func (service *Service) executeRun(ctx context.Context, run AutomationRun) {
	for position := range run.Snapshot.Steps {
		step := run.Snapshot.Steps[position]
		if !service.AdmissionOpen() || service.devices == nil || !service.devices.CommandAdmissionOpen() {
			// Drain closed admission before this Step reserved any Command, so
			// there is deliberately no Command link to expose.
			service.stopRunForDrain(ctx, run.ID, position)
			return
		}
		start, admitted := service.beginStep(ctx, run.ID, position)
		if !admitted {
			service.latchExecutorFault(ctx, run.ID, position)
			return
		}
		// Process-owned: detached from the HTTP request and NATS callback, so
		// caller cancellation cannot cancel an admitted Command.
		_, executionErr := service.devices.ExecuteCommand(ctx, devices.CommandInput{
			ID:            start.CommandID,
			CorrelationID: start.CorrelationID,
			EntityID:      step.EntityID,
			OperationName: step.OperationName,
			Parameters:    step.Parameters,
		})
		completion, established := service.reconcileStep(ctx, step, start, executionErr)
		if !established {
			service.recordExecutorFault(ctx, run.ID, position)
			service.latchExecutorFault(ctx, run.ID, position)
			return
		}
		completion.RunID = run.ID
		completion.Position = position
		if err := service.completeStep(ctx, completion); err != nil {
			service.latchExecutorFault(ctx, run.ID, position)
			return
		}
		//exhaustive:ignore -- reconcileStep returns only satisfied, dispatched, failed, or interrupted.
		switch completion.Status {
		case StepFailed:
			service.completeRun(ctx, run.ID, RunFailed, completion.FailureCode)
			return
		case StepInterrupted:
			service.completeRun(ctx, run.ID, RunInterrupted, completion.FailureCode)
			return
		}
	}
	service.completeRun(ctx, run.ID, RunSucceeded, nil)
}

// beginStep reserves and durably records one Step's Command identity before the
// external call. It reports false when the write cannot be committed, leaving
// the still-running durable row for startup interruption to classify.
func (service *Service) beginStep(
	ctx context.Context,
	runID AutomationRunID,
	position int,
) (StepStart, bool) {
	commandID, err := service.dependencies.NewCommandID()
	if err != nil {
		return StepStart{}, false
	}
	correlationID, err := service.dependencies.NewCorrelationID()
	if err != nil {
		return StepStart{}, false
	}
	start := StepStart{
		RunID:         runID,
		Position:      position,
		CommandID:     commandID,
		CorrelationID: correlationID,
	}
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	if err = service.repository.MarkStepRunning(writeContext, start); err != nil {
		return StepStart{}, false
	}
	return start, true
}

// reconcileStep establishes one Step's terminal outcome. A devices
// CommandExecutionError proves a Command was created, but even a successful
// return is verified against durable ownership before any link is exposed.
func (service *Service) reconcileStep(
	ctx context.Context,
	step AutomationStep,
	start StepStart,
	executionErr error,
) (StepCompletion, bool) {
	readContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	record, err := service.devices.GetCommand(readContext, start.CommandID)
	if errors.Is(err, devices.ErrCommandNotFound) {
		if executionErr == nil {
			// A successful return without a durable Command is unverifiable.
			return StepCompletion{}, false
		}
		if _, created := errors.AsType[*devices.CommandExecutionError](executionErr); created {
			// The Command was created but cannot be read: unverifiable.
			return StepCompletion{}, false
		}
		return preCreationFailure(executionErr), true
	}
	if err != nil {
		return StepCompletion{}, false
	}
	if !stepOwnsCommand(step, start, record) {
		// A collision must never adopt an unrelated Command.
		return StepCompletion{}, false
	}
	if record.CompletedAt == nil {
		return StepCompletion{}, false
	}
	//exhaustive:ignore -- enumerated below with an explicit default handling every failure status.
	switch record.Status {
	case devices.CommandStatusSatisfied:
		verified := record.ID
		return StepCompletion{Status: StepSatisfied, VerifiedCommandID: &verified}, true
	case devices.CommandStatusDispatched:
		verified := record.ID
		return StepCompletion{Status: StepDispatched, VerifiedCommandID: &verified}, true
	case devices.CommandStatusRequested, devices.CommandStatusAccepted:
		return StepCompletion{}, false
	default:
		code := AutomationFailureInternalError
		if record.FailureCode != nil {
			code = string(*record.FailureCode)
		}
		return StepCompletion{Status: StepFailed, FailureCode: &code}, true
	}
}

// preCreationFailure classifies one failure that created no Command. Command
// admission closing is an interruption; a rejected definition or a vanished
// Entity is a normal Step failure.
func preCreationFailure(executionErr error) StepCompletion {
	switch {
	case errors.Is(executionErr, devices.ErrCommandUnavailable):
		code := AutomationFailureCoreStopping
		return StepCompletion{Status: StepInterrupted, FailureCode: &code}
	case errors.Is(executionErr, devices.ErrInvalidCommand), errors.Is(executionErr, devices.ErrCommandIDConflict):
		code := AutomationFailureInvalidCommand
		return StepCompletion{Status: StepFailed, FailureCode: &code}
	case errors.Is(executionErr, devices.ErrEntityNotFound):
		code := AutomationFailureEntityNotFound
		return StepCompletion{Status: StepFailed, FailureCode: &code}
	default:
		code := AutomationFailureInternalError
		return StepCompletion{Status: StepFailed, FailureCode: &code}
	}
}

// stepOwnsCommand verifies durable ownership: the created Command must carry
// exactly the reserved identity, Entity, Operation, and normalized parameters.
func stepOwnsCommand(step AutomationStep, start StepStart, record devices.CommandRecord) bool {
	return record.ID == start.CommandID &&
		record.CorrelationID == start.CorrelationID &&
		record.EntityID == step.EntityID &&
		record.OperationName == step.OperationName &&
		bytes.Equal(record.Parameters, step.Parameters)
}

func (service *Service) completeStep(ctx context.Context, completion StepCompletion) error {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	return service.repository.CompleteStep(writeContext, completion)
}

func (service *Service) completeRun(
	ctx context.Context,
	runID AutomationRunID,
	status RunStatus,
	failureCode *string,
) {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	if err := service.repository.CompleteRun(writeContext, RunCompletion{
		RunID:       runID,
		Status:      status,
		FailureCode: failureCode,
	}); err != nil {
		service.latchExecutorFault(ctx, runID, 0)
		return
	}
	service.dependencies.Logger.InfoContext(
		ctx,
		"automation run completed",
		slog.String("event", "automation.run_completed"),
		slog.String("run_id", string(runID)),
		slog.String("status", string(status)),
	)
}

// stopRunForDrain marks one not-yet-started Step and its Run interrupted with
// core_stopping and no Command link.
func (service *Service) stopRunForDrain(ctx context.Context, runID AutomationRunID, position int) {
	code := AutomationFailureCoreStopping
	if err := service.completeStep(ctx, StepCompletion{
		RunID:       runID,
		Position:    position,
		Status:      StepInterrupted,
		FailureCode: &code,
	}); err != nil {
		service.latchExecutorFault(ctx, runID, position)
		return
	}
	service.completeRun(ctx, runID, RunInterrupted, &code)
}

// recordExecutorFault persists the truthful interrupted/executor_fault outcome
// when it can, without inventing a Command link. A failed write deliberately
// leaves the running rows for startup interruption to classify.
func (service *Service) recordExecutorFault(ctx context.Context, runID AutomationRunID, position int) {
	code := AutomationFailureExecutorFault
	if err := service.completeStep(ctx, StepCompletion{
		RunID:       runID,
		Position:    position,
		Status:      StepInterrupted,
		FailureCode: &code,
	}); err != nil {
		return
	}
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	_ = service.repository.CompleteRun(writeContext, RunCompletion{
		RunID:       runID,
		Status:      RunInterrupted,
		FailureCode: &code,
	})
	service.dependencies.Logger.ErrorContext(
		ctx,
		"automation executor fault recorded",
		slog.String("event", "automation.executor_fault"),
		slog.String("run_id", string(runID)),
		slog.Int("step_position", position),
		slog.String("error_code", code),
	)
}
