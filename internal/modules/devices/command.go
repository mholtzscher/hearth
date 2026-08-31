package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func (service *Service) ExecuteCommand(
	ctx context.Context,
	entityID EntityID,
	operationName OperationName,
	parameters CommandParameters,
) (CommandResult, error) {
	if _, err := ParseEntityID(string(entityID)); err != nil {
		return CommandResult{}, fmt.Errorf("%w: parse entity ID: %w", ErrInvalidCommand, err)
	}
	if !operationNamePattern.MatchString(string(operationName)) {
		return CommandResult{}, fmt.Errorf("%w: operation name is not subject-safe", ErrInvalidCommand)
	}
	if err := validateCommandParameters(parameters); err != nil {
		return CommandResult{}, fmt.Errorf("%w: %w", ErrInvalidCommand, err)
	}
	view, err := service.repository.GetEntity(ctx, entityID)
	if err != nil {
		return CommandResult{}, err
	}
	resolved, err := service.catalog.ResolveCommand(view.Entity, operationName, parameters)
	if err != nil {
		return CommandResult{}, fmt.Errorf("%w: %w", ErrInvalidCommand, err)
	}
	commandID, err := service.dependencies.NewCommandID()
	if err != nil {
		return CommandResult{}, fmt.Errorf("generate command ID: %w", err)
	}
	if _, parseErr := ParseCommandID(string(commandID)); parseErr != nil {
		return CommandResult{}, fmt.Errorf("generate command ID: %w", parseErr)
	}
	correlationID, err := service.dependencies.NewCorrelationID()
	if err != nil {
		return CommandResult{}, fmt.Errorf("generate correlation ID: %w", err)
	}
	if _, parseErr := ParseCorrelationID(string(correlationID)); parseErr != nil {
		return CommandResult{}, fmt.Errorf("generate correlation ID: %w", parseErr)
	}
	requestedAt, err := service.now()
	if err != nil {
		return CommandResult{}, err
	}
	command := CommandRecord{
		ID: commandID, EntityID: entityID, AdapterID: view.Entity.AdapterID,
		OperationName: operationName, Parameters: append(CommandParameters(nil), resolved.Parameters...),
		CorrelationID: correlationID, Status: CommandStatusRequested,
		RequestedAt: requestedAt, DeadlineAt: requestedAt.Add(resolved.Deadline),
	}
	command, err = service.repository.CreateCommand(ctx, command)
	if err != nil {
		return CommandResult{}, err
	}
	if command.Status == CommandStatusEntityDisabled {
		return CommandResult{}, commandExecutionError(command.ID, ErrEntityDisabled)
	}
	if command.Status == CommandStatusAdapterUnhealthy {
		return CommandResult{}, commandExecutionError(command.ID, ErrAdapterUnhealthy)
	}
	waiter := service.addCommandWaiter(command.ID)

	completed := make(chan commandOutcome, 1)
	lifecycleParent := context.WithoutCancel(ctx)
	lifecycleContext, cancel := context.WithDeadline(lifecycleParent, command.DeadlineAt)
	go func() {
		defer cancel()
		defer service.removeCommandWaiter(command.ID)
		completed <- service.runCommand(lifecycleContext, command, waiter)
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
	waiter <-chan CommandResult,
) commandOutcome {
	acceptance, err := service.dispatchCommand(ctx, command)
	if err != nil {
		status, failureCode, outcome := classifyCommandDispatchError(err)
		return service.failCommand(command.ID, status, failureCode, outcome, waiter)
	}
	if !acceptance.Accepted {
		return service.failCommand(
			command.ID,
			CommandStatusRejected,
			CommandFailureUpstreamRejected,
			ErrUpstreamRejected,
			waiter,
		)
	}

	acceptedAt, err := service.now()
	if err != nil {
		return service.failCommand(command.ID, CommandStatusInternalFailure, CommandFailureInternalError, err, waiter)
	}
	writeContext, cancel := persistenceContext(ctx)
	err = service.repository.MarkCommandAccepted(writeContext, command.ID, acceptedAt)
	cancel()
	if err != nil && !errors.Is(err, ErrCommandTerminal) {
		return service.failCommand(command.ID, CommandStatusInternalFailure, CommandFailureInternalError, err, waiter)
	}

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
				command.ID,
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
		completionErr := service.repository.CompleteCommand(completionContext, completion)
		cancelCompletion()
		if errors.Is(completionErr, ErrCommandTerminal) {
			// A matching Observation committed first and its notification follows
			// that transaction. Preserve the satisfying terminal result.
			select {
			case result := <-waiter:
				return commandOutcome{result: result}
			case <-time.After(commandPersistenceTimeout):
				return commandOutcome{
					err: commandExecutionError(command.ID, errors.New("terminal command outcome was not delivered")),
				}
			}
		}
		if completionErr != nil {
			return service.failCommand(
				command.ID,
				CommandStatusInternalFailure,
				CommandFailureInternalError,
				completionErr,
				waiter,
			)
		}
		return commandOutcome{err: commandExecutionError(command.ID, ErrOutcomeTimeout)}
	}
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
	return service.sender.Send(ctx, command.AdapterID, *command.RuntimeID, CommandRequest{
		ID: command.ID, CorrelationID: command.CorrelationID, EntityID: command.EntityID,
		OperationName: command.OperationName, Parameters: append(CommandParameters(nil), command.Parameters...),
		Deadline: command.DeadlineAt,
	})
}

func (service *Service) failCommand(
	id CommandID,
	status CommandStatus,
	failureCode CommandFailureCode,
	outcome error,
	waiter <-chan CommandResult,
) commandOutcome {
	completedAt, err := service.now()
	if err != nil {
		return commandOutcome{err: commandExecutionError(id, err)}
	}
	writeContext, cancel := persistenceContext(context.Background())
	err = service.repository.CompleteCommand(writeContext, CommandCompletion{
		ID: id, Status: status, CompletedAt: completedAt, FailureCode: failureCode,
	})
	cancel()
	if errors.Is(err, ErrCommandTerminal) {
		// A matching Observation committed first and its notification follows
		// that transaction. Preserve the satisfying terminal result.
		select {
		case result := <-waiter:
			return commandOutcome{result: result}
		case <-time.After(commandPersistenceTimeout):
			return commandOutcome{
				err: commandExecutionError(id, errors.New("terminal command outcome was not delivered")),
			}
		}
	}
	if err != nil {
		return commandOutcome{err: commandExecutionError(id, err)}
	}
	return commandOutcome{err: commandExecutionError(id, outcome)}
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
