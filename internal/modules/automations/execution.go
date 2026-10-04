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

// executeRun executes one immutable Run snapshot sequentially. The next Step
// starts only after the prior Command reaches a successful terminal outcome, and
// the first failure or interruption stops the Run without retry.
func (service *Service) executeRun(ctx context.Context, run Run) {
	positions := make(map[StepID]int)
	// Admission already allocated these stable leaf positions. Use the retained
	// attempts so a malformed branch payload can reach the Run-only fault path.
	for _, attempt := range run.Steps {
		positions[attempt.StepID] = attempt.Position
	}
	decisionPosition := 0
	if service.executeSequence(ctx, run, run.Snapshot.Steps, positions, &decisionPosition) {
		service.completeRun(ctx, run.ID, SucceededRun{})
	}
}

func (service *Service) executeCommand(
	ctx context.Context,
	run Run,
	stepID StepID,
	command CommandStep,
	position int,
) bool {
	if position < 0 || position >= len(run.Steps) || run.Steps[position].StepID != stepID {
		service.interruptBranchRun(ctx, run.ID, stepID, FailureExecutorFault)
		return false
	}
	if !service.AdmissionOpen() || service.devices == nil || !service.devices.CommandAdmissionOpen() {
		// Drain closed admission before this Step reserved any Command, so
		// there is deliberately no Command link to expose.
		service.stopRunForDrain(ctx, run.ID, position)
		return false
	}
	start, admitted := service.beginStep(ctx, run.ID, position)
	if !admitted {
		service.latchExecutorFault(ctx, run.ID, position)
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
		service.recordExecutorFault(ctx, run.ID, position)
		service.latchExecutorFault(ctx, run.ID, position)
		return false
	}
	completion.RunID = run.ID
	completion.Position = position
	if err := service.completeStep(ctx, completion); err != nil {
		service.latchExecutorFault(ctx, run.ID, position)
		return false
	}
	switch outcome := completion.Outcome.(type) {
	case FailedStep:
		service.completeRun(ctx, run.ID, FailedRun{FailureCode: outcome.FailureCode})
		return false
	case InterruptedStep:
		service.completeRun(ctx, run.ID, InterruptedRun{FailureCode: outcome.FailureCode})
		return false
	case SatisfiedStep, DispatchedStep:
		return true
	default:
		service.latchExecutorFault(ctx, run.ID, position)
		return false
	}
}

// beginStep reserves and durably records one Step's Command identity before the external call.
func (service *Service) beginStep(
	ctx context.Context,
	runID RunID,
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
	//exhaustive:ignore -- enumerated below with an explicit default handling every failure status.
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

func (service *Service) completeStep(ctx context.Context, completion StepCompletion) error {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	return service.repository.CompleteStep(writeContext, completion)
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
		service.latchRunExecutorFault(ctx, runID, "")
		return
	}
	service.dependencies.Logger.InfoContext(
		ctx,
		"automation run completed",
		slog.String("event", "automation.run_completed"),
		slog.String("run_id", string(runID)),
		slog.String("status", string(RunOutcomeStatus(outcome))),
	)
}

// stopRunForDrain marks one not-yet-started Step and its Run interrupted with core_stopping.
func (service *Service) stopRunForDrain(ctx context.Context, runID RunID, position int) {
	code := FailureCoreStopping
	if err := service.completeStep(ctx, StepCompletion{
		RunID:    runID,
		Position: position,
		Outcome:  InterruptedStep{FailureCode: code},
	}); err != nil {
		service.latchExecutorFault(ctx, runID, position)
		return
	}
	service.completeRun(ctx, runID, InterruptedRun{FailureCode: code})
}

// recordExecutorFault persists the truthful interrupted/executor_fault outcome
// without inventing a Command link.
func (service *Service) recordExecutorFault(ctx context.Context, runID RunID, position int) {
	code := FailureExecutorFault
	if err := service.completeStep(ctx, StepCompletion{
		RunID:    runID,
		Position: position,
		Outcome:  InterruptedStep{FailureCode: code},
	}); err != nil {
		return
	}
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	_ = service.repository.CompleteRun(writeContext, RunCompletion{
		RunID:   runID,
		Outcome: InterruptedRun{FailureCode: code},
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
