package automations

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// automationPersistenceTimeout bounds one automation-owned SQLite read or write.
// It never wraps the external Command call, which devices owns and deadlines.
const automationPersistenceTimeout = 5 * time.Second

// Stable Step and Run failure codes. They are fixed tokens, never upstream text.
const (
	// FailureCoreStopping marks a Step or Run stopped because Command
	// admission closed during drain.
	FailureCoreStopping = "core_stopping"
	// FailureCoreRestarted marks a Step or Run interrupted by restart.
	FailureCoreRestarted = "core_restarted"
	// FailureExecutorFault marks a Step or Run the executor could not
	// advance truthfully, such as an unverifiable or nonterminal Command.
	FailureExecutorFault = "executor_fault"
	// FailureInvalidCommand marks a Step rejected before Command creation.
	FailureInvalidCommand = "invalid_command"
	// FailureEntityNotFound marks a Step whose Entity vanished before
	// Command creation.
	FailureEntityNotFound = "entity_not_found"
	// FailureInternalError is the fallback for one confirmed Command
	// failure with no more specific durable code.
	FailureInternalError = "internal_error"
)

// interruptRunExecutorFault reports Run/control-flow faults without a command position.
func (service *Service) interruptRunExecutorFault(ctx context.Context, runID RunID, stepID StepID) {
	service.interruptFaultedRun(ctx, runID, slog.String("step_id", string(stepID)))
}

// interruptFaultedRun makes one bounded, atomic cleanup attempt. Failure leaves
// this Automation busy until startup recovery, not the module unavailable.
// Callers stop advancing the Run; cleanup never replays external work.
func (service *Service) interruptFaultedRun(ctx context.Context, runID RunID, location slog.Attr) {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.InterruptRun(writeContext, runID, service.dependencies.Now().UTC())
	cancel()
	service.dependencies.Logger.ErrorContext(ctx, "automation run executor fault",
		slog.String("event", "automation.executor_fault"),
		slog.String("run_id", string(runID)), location,
		slog.String("error_code", FailureExecutorFault))
	if err != nil {
		// Database errors can contain payloads; log only stable diagnostic tokens.
		service.dependencies.Logger.ErrorContext(ctx, "automation run interruption failed; startup recovery required",
			slog.String("event", "automation.run_interruption_failed"),
			slog.String("run_id", string(runID)),
			slog.String("error_code", FailureExecutorFault))
	}
}

// executeRun executes one immutable Run snapshot sequentially. The next Step
// starts only after the prior Command reaches a successful terminal outcome, and
// the first failure or interruption stops the Run without retry.
func (service *Service) executeRun(ctx context.Context, run Run) {
	if service.executeSequence(ctx, run, run.Snapshot.Steps) {
		if !service.executionOpen() {
			service.interruptBranchRun(ctx, run.ID, "", service.executionStopReason())
			return
		}
		service.completeRun(ctx, run.ID, SucceededRun{})
	}
}

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
	start, admitted := service.beginStep(ctx, run.ID, stepID)
	if !admitted {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	// Process-owned: detached from the HTTP request and NATS callback, so
	// caller cancellation cannot cancel an admitted Command.
	_, executionErr := service.devices.ExecuteCommand(
		ctx,
		devices.CommandInput{
			ID:            start.CommandID,
			CorrelationID: start.CorrelationID,
			EntityID:      command.EntityID,
			OperationName: command.OperationName,
			Parameters:    command.Parameters,
		},
	)
	completion, established := service.reconcileStep(ctx, command, start, executionErr)
	if !established {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	if err := service.completeCommandStep(ctx, run.ID, stepID, start, completion); err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	switch outcome := completion.Outcome.(type) {
	case FailedStep:
		service.logRunCompleted(ctx, run.ID, FailedRun{FailureCode: outcome.FailureCode})
		return false
	case InterruptedStep:
		service.logRunCompleted(ctx, run.ID, InterruptedRun{FailureCode: outcome.FailureCode})
		return false
	case SatisfiedStep, DispatchedStep:
		return true
	default:
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
}

// beginStep reserves and durably records one Step's Command identity before the external call.
func (service *Service) beginStep(
	ctx context.Context,
	runID RunID,
	stepID StepID,
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
		StepID:        stepID,
		CommandID:     commandID,
		CorrelationID: correlationID,
	}
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	if err = service.repository.RecordStep(writeContext, StepRecord{
		RunID:  runID,
		StepID: stepID,
		Evidence: CommandStarted{
			StartedAt:   service.dependencies.Now().UTC(),
			Reservation: CommandReservation{CommandID: commandID, CorrelationID: correlationID},
		},
	}); err != nil {
		return StepStart{}, false
	}
	return start, true
}

// reconcileStep establishes one Step's terminal outcome, verifying durable
// ownership even after a successful Command return.
func (service *Service) reconcileStep(
	ctx context.Context,
	command CommandStep,
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
	if !stepOwnsCommand(command, start, record) {
		// A collision must never adopt an unrelated Command.
		return StepCompletion{}, false
	}
	if record.CompletedAt == nil {
		return StepCompletion{}, false
	}
	// The default handles every failure status.
	//exhaustive:ignore
	switch record.Status {
	case devices.CommandStatusSatisfied:
		return StepCompletion{Outcome: SatisfiedStep{VerifiedCommandID: record.ID}}, true
	case devices.CommandStatusDispatched:
		return StepCompletion{Outcome: DispatchedStep{VerifiedCommandID: record.ID}}, true
	case devices.CommandStatusRequested, devices.CommandStatusAccepted:
		return StepCompletion{}, false
	default:
		code := FailureInternalError
		if record.FailureCode != nil {
			code = string(*record.FailureCode)
		}
		return StepCompletion{Outcome: FailedStep{FailureCode: code}}, true
	}
}

// preCreationFailure classifies one failure that created no Command.
func preCreationFailure(executionErr error) StepCompletion {
	switch {
	case errors.Is(executionErr, devices.ErrCommandUnavailable):
		code := FailureCoreStopping
		return StepCompletion{Outcome: InterruptedStep{FailureCode: code}}
	case errors.Is(executionErr, devices.ErrInvalidCommand), errors.Is(executionErr, devices.ErrCommandIDConflict):
		code := FailureInvalidCommand
		return StepCompletion{Outcome: FailedStep{FailureCode: code}}
	case errors.Is(executionErr, devices.ErrEntityNotFound):
		code := FailureEntityNotFound
		return StepCompletion{Outcome: FailedStep{FailureCode: code}}
	default:
		code := FailureInternalError
		return StepCompletion{Outcome: FailedStep{FailureCode: code}}
	}
}

// stepOwnsCommand verifies durable ownership: the created Command must carry
// exactly the reserved identity, Entity, Operation, and normalized parameters.
func stepOwnsCommand(command CommandStep, start StepStart, record devices.CommandRecord) bool {
	return record.ID == start.CommandID &&
		record.CorrelationID == start.CorrelationID &&
		record.EntityID == command.EntityID &&
		record.OperationName == command.OperationName &&
		bytes.Equal(record.Parameters, command.Parameters)
}

func (service *Service) completeCommandStep(
	ctx context.Context,
	runID RunID,
	stepID StepID,
	start StepStart,
	completion StepCompletion,
) error {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	return service.repository.CompleteStep(writeContext, StepExecutionCompletion{
		RunID:  runID,
		StepID: stepID,
		Evidence: CommandFinished{
			CompletedAt: service.dependencies.Now().UTC(),
			Reservation: CommandReservation{CommandID: start.CommandID, CorrelationID: start.CorrelationID},
			Outcome:     completion.Outcome,
		},
	})
}

func (service *Service) completeRun(
	ctx context.Context,
	runID RunID,
	outcome RunOutcome,
) {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	if err := service.repository.CompleteRun(writeContext, RunCompletion{
		RunID:   runID,
		Outcome: outcome,
	}); err != nil {
		service.interruptRunExecutorFault(ctx, runID, "")
		return
	}
	service.logRunCompleted(ctx, runID, outcome)
}

func (service *Service) logRunCompleted(ctx context.Context, runID RunID, outcome RunOutcome) {
	service.dependencies.Logger.InfoContext(
		ctx,
		"automation run completed",
		slog.String("event", "automation.run_completed"),
		slog.String("run_id", string(runID)),
		slog.String("status", string(RunOutcomeStatus(outcome))),
	)
}

// stopRunForDrain interrupts a not-yet-started Command and its Run with the stop cause.
func (service *Service) stopRunForDrain(ctx context.Context, runID RunID, stepID StepID) {
	code := service.executionStopReason()
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.RecordStep(
		writeContext,
		StepRecord{
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
