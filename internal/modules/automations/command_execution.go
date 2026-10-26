package automations

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func (service *Service) executeCommand(
	ctx context.Context,
	run Run,
	stepID StepID,
	command CommandStep,
) bool {
	if !service.executionOpen() {
		// Drain closed admission before this Step reserved any Command, so
		// there is deliberately no Command link to expose.
		service.stopRunForDrain(ctx, run.ID, stepID)
		return false
	}
	reservation, admitted := service.beginCommandStep(ctx, run.ID, stepID)
	if !admitted {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	// Process-owned: detached from the HTTP request and NATS callback, so
	// caller cancellation cannot cancel an admitted Command.
	_, executionErr := service.devices.ExecuteCommand(
		ctx,
		devices.CommandInput{
			ID:            reservation.CommandID,
			CorrelationID: reservation.CorrelationID,
			EntityID:      command.EntityID,
			OperationName: command.OperationName,
			Parameters:    command.Parameters,
		},
	)
	completion, established := service.reconcileCommandStep(ctx, command, reservation, executionErr)
	if !established {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	if err := service.completeCommandStep(ctx, run.ID, stepID, completion); err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	switch outcome := completion.(type) {
	case CommandFailed:
		service.logRunCompleted(ctx, run.ID, FailedRun{FailureCode: outcome.FailureCode})
		return false
	case CommandInterrupted:
		service.logRunCompleted(ctx, run.ID, InterruptedRun{FailureCode: outcome.FailureCode})
		return false
	case CommandSucceeded:
		return true
	default:
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
}

// beginCommandStep reserves and durably records one Step's Command identity before the external call.
func (service *Service) beginCommandStep(
	ctx context.Context,
	runID RunID,
	stepID StepID,
) (CommandReservation, bool) {
	commandID, err := service.dependencies.NewCommandID()
	if err != nil {
		return CommandReservation{}, false
	}
	correlationID, err := service.dependencies.NewCorrelationID()
	if err != nil {
		return CommandReservation{}, false
	}
	reservation := CommandReservation{CommandID: commandID, CorrelationID: correlationID}
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	if err = service.repository.RecordStep(writeContext, StepExecutionRecord{
		RunID:    runID,
		StepID:   stepID,
		Evidence: CommandStarted{StartedAt: service.dependencies.Now().UTC(), Reservation: reservation},
	}); err != nil {
		return CommandReservation{}, false
	}
	return reservation, true
}

// reconcileCommandStep establishes one Step's terminal outcome, verifying durable
// ownership even after a successful Command return.
func (service *Service) reconcileCommandStep(
	ctx context.Context,
	command CommandStep,
	reservation CommandReservation,
	executionErr error,
) (CommandTerminalState, bool) {
	readContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	record, err := service.devices.GetCommand(readContext, reservation.CommandID)
	if errors.Is(err, devices.ErrCommandNotFound) {
		if executionErr == nil {
			// A successful return without a durable Command is unverifiable.
			return nil, false
		}
		if _, created := errors.AsType[*devices.CommandExecutionError](executionErr); created {
			// The Command was created but cannot be read: unverifiable.
			return nil, false
		}
		return preCreationFailure(reservation, service.dependencies.Now().UTC(), executionErr), true
	}
	if err != nil {
		return nil, false
	}
	if !stepOwnsCommand(command, reservation, record) {
		// A collision must never adopt an unrelated Command.
		return nil, false
	}
	if record.CompletedAt == nil {
		return nil, false
	}
	// The default handles every failure status.
	//exhaustive:ignore
	switch record.Status {
	case devices.CommandStatusSatisfied:
		return CommandSucceeded{
			CompletedAt: service.dependencies.Now().UTC(),
			Reservation: reservation,
			Outcome:     SatisfiedCommand{VerifiedCommandID: record.ID},
		}, true
	case devices.CommandStatusDispatched:
		return CommandSucceeded{
			CompletedAt: service.dependencies.Now().UTC(),
			Reservation: reservation,
			Outcome:     DispatchedCommand{VerifiedCommandID: record.ID},
		}, true
	case devices.CommandStatusRequested, devices.CommandStatusAccepted:
		return nil, false
	default:
		code := FailureInternalError
		if record.FailureCode != nil {
			code = string(*record.FailureCode)
		}
		return CommandFailed{
			CompletedAt: service.dependencies.Now().UTC(),
			Reservation: reservation,
			FailureCode: code,
		}, true
	}
}

// preCreationFailure classifies one failure that created no Command.
func preCreationFailure(
	reservation CommandReservation,
	completedAt time.Time,
	executionErr error,
) CommandTerminalState {
	switch {
	case errors.Is(executionErr, devices.ErrCommandUnavailable):
		code := FailureCoreStopping
		return CommandInterrupted{CompletedAt: completedAt, Reservation: &reservation, FailureCode: code}
	case errors.Is(executionErr, devices.ErrInvalidCommand), errors.Is(executionErr, devices.ErrCommandIDConflict):
		code := FailureInvalidCommand
		return CommandFailed{CompletedAt: completedAt, Reservation: reservation, FailureCode: code}
	case errors.Is(executionErr, devices.ErrEntityNotFound):
		code := FailureEntityNotFound
		return CommandFailed{CompletedAt: completedAt, Reservation: reservation, FailureCode: code}
	default:
		code := FailureInternalError
		return CommandFailed{CompletedAt: completedAt, Reservation: reservation, FailureCode: code}
	}
}

// stepOwnsCommand verifies durable ownership: the created Command must carry
// exactly the reserved identity, Entity, Operation, and normalized parameters.
func stepOwnsCommand(command CommandStep, reservation CommandReservation, record devices.CommandRecord) bool {
	return record.ID == reservation.CommandID &&
		record.CorrelationID == reservation.CorrelationID &&
		record.EntityID == command.EntityID &&
		record.OperationName == command.OperationName &&
		bytes.Equal(record.Parameters, command.Parameters)
}

func (service *Service) completeCommandStep(
	ctx context.Context,
	runID RunID,
	stepID StepID,
	completion CommandTerminalState,
) error {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	return service.repository.CompleteStep(writeContext, StepExecutionCompletion{
		RunID:    runID,
		StepID:   stepID,
		Evidence: CommandFinished{State: completion},
	})
}

// stopRunForDrain interrupts a not-yet-started Command and its Run with the stop cause.
func (service *Service) stopRunForDrain(ctx context.Context, runID RunID, stepID StepID) {
	code := service.executionStopReason()
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.RecordStep(
		writeContext,
		StepExecutionRecord{
			RunID:    runID,
			StepID:   stepID,
			Evidence: CommandStopped{At: service.dependencies.Now().UTC(), FailureCode: code},
		},
	)
	cancel()
	if err != nil {
		service.interruptRunExecutorFault(ctx, runID, stepID)
		return
	}
	service.logRunCompleted(ctx, runID, InterruptedRun{FailureCode: code})
}
