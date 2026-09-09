package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const commandPersistenceTimeout = 5 * time.Second

// CommandExecutionError identifies a Command that was durably created before
// its lifecycle failed.
type CommandExecutionError struct {
	CommandID CommandID
	Err       error
}

func (err *CommandExecutionError) Error() string {
	return fmt.Sprintf("command %s: %v", err.CommandID, err.Err)
}

func (err *CommandExecutionError) Unwrap() error { return err.Err }

// ValidateCommand validates current support without generating identities, writing,
// checking control eligibility, or dispatching. Returned parameters are owned by the caller.
func (service *Service) ValidateCommand(ctx context.Context, input CommandInput) (CommandParameters, error) {
	_, resolved, err := service.validateCommand(ctx, input)
	if err != nil {
		return nil, err
	}
	return append(CommandParameters(nil), resolved.Parameters...), nil
}

func (service *Service) validateCommand(ctx context.Context, input CommandInput) (Entity, ResolvedCommand, error) {
	if input.ID != "" {
		if _, err := ParseCommandID(string(input.ID)); err != nil {
			return Entity{}, ResolvedCommand{}, fmt.Errorf("%w: parse command ID: %w", ErrInvalidCommand, err)
		}
	}
	if input.CorrelationID != "" {
		if _, err := ParseCorrelationID(string(input.CorrelationID)); err != nil {
			return Entity{}, ResolvedCommand{}, fmt.Errorf("%w: parse correlation ID: %w", ErrInvalidCommand, err)
		}
	}
	if _, err := ParseEntityID(string(input.EntityID)); err != nil {
		return Entity{}, ResolvedCommand{}, fmt.Errorf("%w: parse entity ID: %w", ErrInvalidCommand, err)
	}
	if !operationNamePattern.MatchString(string(input.OperationName)) {
		return Entity{}, ResolvedCommand{}, fmt.Errorf("%w: operation name is not subject-safe", ErrInvalidCommand)
	}
	if err := validateCommandParameters(input.Parameters); err != nil {
		return Entity{}, ResolvedCommand{}, fmt.Errorf("%w: %w", ErrInvalidCommand, err)
	}
	view, err := service.stores.Reads.GetEntity(ctx, input.EntityID)
	if err != nil {
		return Entity{}, ResolvedCommand{}, err
	}
	resolved, err := service.catalog.ResolveCommand(view.Entity, input.OperationName, input.Parameters)
	if err != nil {
		return Entity{}, ResolvedCommand{}, fmt.Errorf("%w: %w", ErrInvalidCommand, err)
	}
	return view.Entity, resolved, nil
}

// ExecuteCommand persists command identity before dispatch; caller cancellation
// stops waiting but does not cancel a durably created command.
func (service *Service) ExecuteCommand(ctx context.Context, input CommandInput) (CommandResult, error) {
	entity, resolved, err := service.validateCommand(ctx, input)
	if err != nil {
		return CommandResult{}, err
	}
	commandID := input.ID
	if commandID == "" {
		commandID, err = service.dependencies.NewCommandID()
		if err != nil {
			return CommandResult{}, fmt.Errorf("generate command ID: %w", err)
		}
		if _, parseErr := ParseCommandID(string(commandID)); parseErr != nil {
			return CommandResult{}, fmt.Errorf("generate command ID: %w", parseErr)
		}
	}
	correlationID := input.CorrelationID
	if correlationID == "" {
		correlationID, err = service.dependencies.NewCorrelationID()
		if err != nil {
			return CommandResult{}, fmt.Errorf("generate correlation ID: %w", err)
		}
		if _, parseErr := ParseCorrelationID(string(correlationID)); parseErr != nil {
			return CommandResult{}, fmt.Errorf("generate correlation ID: %w", parseErr)
		}
	}
	requestedAt, err := service.now()
	if err != nil {
		return CommandResult{}, err
	}
	command := CommandRecord{
		ID: commandID, EntityID: input.EntityID, AdapterID: entity.AdapterID,
		OperationName: input.OperationName, Parameters: append(CommandParameters(nil), resolved.Parameters...),
		CorrelationID: correlationID, Status: CommandStatusRequested,
		RequestedAt: requestedAt, DeadlineAt: requestedAt.Add(resolved.Deadline),
	}
	command, err = service.stores.Commands.CreateCommand(ctx, command)
	if err != nil {
		return CommandResult{}, err
	}
	if command.Status == CommandStatusEntityDisabled {
		service.logCommandCreated(ctx, command)
		return CommandResult{}, commandExecutionError(command.ID, ErrEntityDisabled)
	}
	if command.Status == CommandStatusAdapterUnhealthy {
		service.logCommandCreated(ctx, command)
		return CommandResult{}, commandExecutionError(command.ID, ErrAdapterUnhealthy)
	}
	waiter := service.addCommandWaiter(command.ID)

	completed := make(chan commandOutcome, 1)
	lifecycleParent := context.WithoutCancel(ctx)
	lifecycleContext, cancel := context.WithDeadline(lifecycleParent, command.DeadlineAt)
	go func() {
		completed <- service.runCommand(lifecycleContext, command, resolved.Outcome, waiter)
		service.removeCommandWaiter(command.ID)
		cancel()
		// Publish the outcome and release lifecycle resources before a slow
		// diagnostic sink can block this existing worker.
		service.logCommandCreated(lifecycleParent, command)
	}()

	select {
	case outcome := <-completed:
		return outcome.result, outcome.err
	default:
	}
	select {
	case outcome := <-completed:
		return outcome.result, outcome.err
	case <-ctx.Done():
		return CommandResult{}, ctx.Err()
	}
}

type commandOutcome struct {
	result CommandResult
	err    error
}

func classifyCommandDispatchError(err error) (CommandStatus, CommandFailureCode, error) {
	switch {
	case errors.Is(err, ErrEntityUnavailable):
		return CommandStatusEntityUnavailable, CommandFailureEntityUnavailable, ErrEntityUnavailable
	case errors.Is(err, ErrAdapterUnhealthy), errors.Is(err, context.DeadlineExceeded):
		return CommandStatusAdapterUnhealthy, CommandFailureAdapterUnhealthy, ErrAdapterUnhealthy
	default:
		return CommandStatusInternalFailure, CommandFailureInternalError, err
	}
}

func (service *Service) runCommand(
	ctx context.Context,
	command CommandRecord,
	outcome OutcomeKind,
	waiter <-chan CommandResult,
) commandOutcome {
	acceptance, err := service.dispatchCommand(ctx, command)
	if err != nil {
		status, failureCode, outcome := classifyCommandDispatchError(err)
		return service.failCommand(ctx, command, status, failureCode, outcome, waiter)
	}
	if !acceptance.Accepted {
		return service.failCommand(
			ctx,
			command,
			CommandStatusRejected,
			CommandFailureUpstreamRejected,
			ErrUpstreamRejected,
			waiter,
		)
	}

	acceptedAt, err := service.now()
	if err != nil {
		return service.failCommand(ctx, command, CommandStatusInternalFailure, CommandFailureInternalError, err, waiter)
	}
	writeContext, cancel := persistenceContext(ctx)
	err = service.stores.Commands.MarkCommandAccepted(writeContext, command.ID, acceptedAt)
	cancel()
	if err != nil && !errors.Is(err, ErrCommandTerminal) {
		return service.failCommand(ctx, command, CommandStatusInternalFailure, CommandFailureInternalError, err, waiter)
	}

	if outcome == OutcomeDispatched {
		// Persist CommandStatusDispatched with an empty failure code, then
		// return the constructor-validated result with no observation/value.
		return service.completeDispatchedCommand(ctx, command, waiter)
	}
	// OutcomeObserved alone continues into the existing waiter/timeout path.

	select {
	case result := <-waiter:
		return commandOutcome{result: result}
	default:
	}
	select {
	case result := <-waiter:
		return commandOutcome{result: result}
	case <-ctx.Done():
		completedAt, nowErr := service.now()
		if nowErr != nil {
			return service.failCommand(
				ctx,
				command,
				CommandStatusInternalFailure,
				CommandFailureInternalError,
				nowErr,
				waiter,
			)
		}
		completion := CommandCompletion{
			ID: command.ID, Status: CommandStatusOutcomeTimeout, CompletedAt: completedAt,
			FailureCode: CommandFailureOutcomeTimeout,
		}
		completionContext, cancelCompletion := persistenceContext(ctx)
		completionErr := service.stores.Commands.CompleteCommand(completionContext, completion)
		cancelCompletion()
		if errors.Is(completionErr, ErrCommandTerminal) {
			// A matching Observation committed first and its notification follows
			// that transaction. Preserve the satisfying terminal result.
			select {
			case result := <-waiter:
				return commandOutcome{result: result}
			case <-time.After(commandPersistenceTimeout):
				// No durable outcome was established here and the satisfying
				// result never arrived; the caller may already be gone, so log
				// the diagnostic instead of inventing a terminal status.
				service.logCommandExecutionFailed(ctx, command)
				return commandOutcome{
					err: commandExecutionError(command.ID, errors.New("terminal command outcome was not delivered")),
				}
			}
		}
		if completionErr != nil {
			return service.failCommand(
				ctx,
				command,
				CommandStatusInternalFailure,
				CommandFailureInternalError,
				completionErr,
				waiter,
			)
		}
		return commandOutcome{err: commandExecutionError(command.ID, ErrOutcomeTimeout)}
	}
}

// completeDispatchedCommand durably commits the dispatched terminal outcome
// after adapter acceptance. It is part of the existing runCommand lifecycle,
// not a second lifecycle: acceptance is already persisted above, and this
// step only commits the terminal record. An idempotent same completion is
// success; a competing terminal completion keeps the existing
// ErrCommandTerminal waiter-drain behavior.
func (service *Service) completeDispatchedCommand(
	ctx context.Context,
	command CommandRecord,
	waiter <-chan CommandResult,
) commandOutcome {
	completedAt, err := service.now()
	if err != nil {
		// The completion clock left no durable outcome; the caller may already
		// be gone, so log the diagnostic instead of inventing a status.
		service.logCommandExecutionFailed(ctx, command)
		return commandOutcome{err: commandExecutionError(command.ID, err)}
	}
	writeContext, cancel := persistenceContext(ctx)
	err = service.stores.Commands.CompleteCommand(writeContext, CommandCompletion{
		ID: command.ID, Status: CommandStatusDispatched, CompletedAt: completedAt,
	})
	cancel()
	if errors.Is(err, ErrCommandTerminal) {
		// A competing terminal completion committed first. Preserve whatever
		// durable outcome its notification carries.
		select {
		case result := <-waiter:
			return commandOutcome{result: result}
		case <-time.After(commandPersistenceTimeout):
			service.logCommandExecutionFailed(ctx, command)
			return commandOutcome{
				err: commandExecutionError(command.ID, errors.New("terminal command outcome was not delivered")),
			}
		}
	}
	if err != nil {
		service.logCommandExecutionFailed(ctx, command)
		return commandOutcome{err: commandExecutionError(command.ID, err)}
	}
	result, err := NewCommandResult(command.ID, OutcomeDispatched, nil, nil)
	if err != nil {
		service.logCommandExecutionFailed(ctx, command)
		return commandOutcome{err: commandExecutionError(command.ID, err)}
	}
	return commandOutcome{result: result}
}

func (service *Service) dispatchCommand(
	ctx context.Context,
	command CommandRecord,
) (CommandAcceptance, error) {
	if service.sender == nil {
		return CommandAcceptance{}, errors.New("command sender is not configured")
	}
	if command.RuntimeID == nil {
		return CommandAcceptance{}, ErrAdapterUnhealthy
	}
	service.commandScopedLogger(command).DebugContext(ctx, "command dispatching",
		slog.String(commandEventKey, "command.dispatched"),
	)
	return service.sender.Send(ctx, command.AdapterID, *command.RuntimeID, CommandRequest{
		ID: command.ID, CorrelationID: command.CorrelationID, EntityID: command.EntityID,
		OperationName: command.OperationName, Parameters: append(CommandParameters(nil), command.Parameters...),
		Deadline: command.DeadlineAt,
	})
}

func (service *Service) failCommand(
	ctx context.Context,
	command CommandRecord,
	status CommandStatus,
	failureCode CommandFailureCode,
	outcome error,
	waiter <-chan CommandResult,
) commandOutcome {
	completedAt, err := service.now()
	if err != nil {
		// The failure clock left no durable outcome; the caller may already
		// be gone, so log the diagnostic instead of inventing a status.
		service.logCommandExecutionFailed(ctx, command)
		return commandOutcome{err: commandExecutionError(command.ID, err)}
	}
	writeContext, cancel := persistenceContext(context.Background())
	err = service.stores.Commands.CompleteCommand(writeContext, CommandCompletion{
		ID: command.ID, Status: status, CompletedAt: completedAt, FailureCode: failureCode,
	})
	cancel()
	if errors.Is(err, ErrCommandTerminal) {
		// A matching Observation committed first and its notification follows
		// that transaction. Preserve the satisfying terminal result.
		select {
		case result := <-waiter:
			return commandOutcome{result: result}
		case <-time.After(commandPersistenceTimeout):
			// No durable outcome was established here and the satisfying
			// result never arrived; the caller may already be gone, so log
			// the diagnostic instead of inventing a terminal status.
			service.logCommandExecutionFailed(ctx, command)
			return commandOutcome{
				err: commandExecutionError(command.ID, errors.New("terminal command outcome was not delivered")),
			}
		}
	}
	if err != nil {
		// The persistence write left no durable outcome; the caller may
		// already be gone, so log the diagnostic instead of inventing
		// a status.
		service.logCommandExecutionFailed(ctx, command)
		return commandOutcome{err: commandExecutionError(command.ID, err)}
	}
	if status == CommandStatusInternalFailure {
		// The internal failure persisted, but the caller may already be
		// gone and the returned error swallowed, so leave a safe
		// diagnostic. No status is logged: the durable outcome belongs
		// to the command API and persistence.
		service.logCommandExecutionFailed(ctx, command)
	}
	return commandOutcome{err: commandExecutionError(command.ID, outcome)}
}

func (service *Service) now() (time.Time, error) {
	now := service.dependencies.Now().UTC()
	if now.IsZero() {
		return time.Time{}, errors.New("command clock returned zero time")
	}
	return now, nil
}

func (service *Service) addCommandWaiter(id CommandID) chan CommandResult {
	service.waiters.mutex.Lock()
	defer service.waiters.mutex.Unlock()
	waiter := make(chan CommandResult, 1)
	service.waiters.byID[id] = waiter
	return waiter
}

func (service *Service) removeCommandWaiter(id CommandID) {
	service.waiters.mutex.Lock()
	defer service.waiters.mutex.Unlock()
	delete(service.waiters.byID, id)
}

func (service *Service) notifyCommand(result CommandResult) {
	service.waiters.mutex.Lock()
	waiter := service.waiters.byID[result.CommandID]
	service.waiters.mutex.Unlock()
	if waiter == nil {
		return
	}
	select {
	case waiter <- result:
	default:
	}
}

func persistenceContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), commandPersistenceTimeout)
}

func commandExecutionError(id CommandID, err error) error {
	return &CommandExecutionError{CommandID: id, Err: err}
}

func validateCommandParameters(parameters CommandParameters) error {
	if !json.Valid(parameters) {
		return errors.New("parameters must contain one valid JSON value")
	}
	decoder := json.NewDecoder(bytes.NewReader(parameters))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return errors.New("parameters must be a JSON object")
	}
	return nil
}
