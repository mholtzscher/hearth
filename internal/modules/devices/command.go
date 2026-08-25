package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (service *Service) ExecuteCommand(
	ctx context.Context,
	entityID EntityID,
	operationName OperationName,
	parameters CommandParameters,
) (CommandResult, error) {
	done, err := service.beginWork(ctx, false)
	if err != nil {
		return CommandResult{}, err
	}
	transferred := false
	defer func() {
		if !transferred {
			done()
		}
	}()
	requestContext, cancelRequest := service.requestContext(ctx)
	defer cancelRequest()

	if _, err := ParseEntityID(string(entityID)); err != nil {
		return CommandResult{}, fmt.Errorf("%w: parse entity ID: %v", ErrInvalidCommand, err)
	}
	if !operationNamePattern.MatchString(string(operationName)) {
		return CommandResult{}, fmt.Errorf("%w: operation name is not subject-safe", ErrInvalidCommand)
	}
	if err := validateCommandParameters(parameters); err != nil {
		return CommandResult{}, fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}
	view, err := getEntityView(requestContext, service.database, entityID)
	if err != nil {
		return CommandResult{}, operationError(requestContext, err)
	}
	resolved, err := service.catalog.resolveCommand(view.Entity, operationName, parameters)
	if err != nil {
		return CommandResult{}, fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}
	commandID, err := service.controls.newCommandID()
	if err != nil {
		return CommandResult{}, fmt.Errorf("generate command ID: %w", err)
	}
	if _, err := ParseCommandID(string(commandID)); err != nil {
		return CommandResult{}, fmt.Errorf("generate command ID: %w", err)
	}
	correlationID, err := service.controls.newCorrelationID()
	if err != nil {
		return CommandResult{}, fmt.Errorf("generate correlation ID: %w", err)
	}
	if _, err := ParseCorrelationID(string(correlationID)); err != nil {
		return CommandResult{}, fmt.Errorf("generate correlation ID: %w", err)
	}
	requestedAt, err := serviceNow(service.controls.now, "Command")
	if err != nil {
		return CommandResult{}, err
	}
	command := commandRecord{
		id: commandID, entityID: entityID, adapterID: view.Entity.AdapterID,
		operationName: operationName, parameters: append(CommandParameters(nil), resolved.parameters...),
		correlationID: correlationID, status: commandStatusRequested,
		requestedAt: requestedAt, deadlineAt: requestedAt.Add(resolved.deadline),
	}
	waiter := service.addCommandWaiter(command.id)
	if err := service.createCommand(requestContext, command); err != nil {
		service.removeCommandWaiter(command.id)
		return CommandResult{}, operationError(requestContext, err)
	}

	workContext, cancelWork := service.workContext(ctx)
	completed := make(chan commandOutcome, 1)
	transferred = true
	go func() {
		defer done()
		defer cancelWork()
		defer service.removeCommandWaiter(command.id)
		deadlineContext, cancelDeadline := service.controls.withDeadline(workContext, command.deadlineAt)
		defer cancelDeadline()
		completed <- service.runCommand(deadlineContext, workContext, command, waiter)
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

func (service *Service) runCommand(
	deadlineContext context.Context,
	workContext context.Context,
	command commandRecord,
	waiter <-chan CommandResult,
) commandOutcome {
	acceptance, err := service.delivery.Deliver(deadlineContext, command.adapterID, CommandDispatch{
		ID: command.id, CorrelationID: command.correlationID, EntityID: command.entityID,
		OperationName: command.operationName, Parameters: append(CommandParameters(nil), command.parameters...),
		Deadline: command.deadlineAt,
	})
	if err != nil {
		if errors.Is(context.Cause(workContext), ErrServiceStopped) {
			return service.stoppedCommand(command.id, waiter)
		}
		if errors.Is(err, ErrAdapterUnavailable) || errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(context.Cause(deadlineContext), context.DeadlineExceeded) {
			return service.failCommand(workContext, command.id, commandStatusAdapterUnavailable, commandFailureAdapterUnavailable, ErrAdapterUnavailable, waiter)
		}
		return service.failCommand(workContext, command.id, commandStatusInternalFailure, commandFailureInternalError, err, waiter)
	}
	if !acceptance.Accepted {
		return service.failCommand(workContext, command.id, commandStatusRejected, commandFailureUpstreamRejected, ErrUpstreamRejected, waiter)
	}

	acceptedAt, err := serviceNow(service.controls.now, "Command")
	if err != nil {
		return service.failCommand(workContext, command.id, commandStatusInternalFailure, commandFailureInternalError, err, waiter)
	}
	writeContext, cancelWrite := service.writeContext(workContext)
	err = service.markCommandAccepted(writeContext, command.id, acceptedAt)
	cancelWrite()
	if err != nil {
		if errors.Is(operationError(writeContext, err), ErrServiceStopped) {
			return service.stoppedCommand(command.id, waiter)
		}
		if !errors.Is(err, errCommandTerminal) {
			return service.failCommand(workContext, command.id, commandStatusInternalFailure, commandFailureInternalError, err, waiter)
		}
		return service.awaitSatisfiedCommand(workContext, command.id, waiter)
	}

	select {
	case result := <-waiter:
		return commandOutcome{result: result}
	default:
	}
	select {
	case result := <-waiter:
		return commandOutcome{result: result}
	case <-deadlineContext.Done():
		if errors.Is(context.Cause(workContext), ErrServiceStopped) {
			return service.stoppedCommand(command.id, waiter)
		}
		completedAt, nowErr := serviceNow(service.controls.now, "Command")
		if nowErr != nil {
			return service.failCommand(workContext, command.id, commandStatusInternalFailure, commandFailureInternalError, nowErr, waiter)
		}
		writeContext, cancelWrite := service.writeContext(workContext)
		err := service.completeCommand(writeContext, commandCompletion{
			id: command.id, status: commandStatusOutcomeTimeout, completedAt: completedAt,
			failureCode: commandFailureOutcomeTimeout,
		})
		cancelWrite()
		if errors.Is(err, errCommandTerminal) {
			return service.awaitSatisfiedCommand(workContext, command.id, waiter)
		}
		if err != nil {
			if errors.Is(operationError(writeContext, err), ErrServiceStopped) {
				return service.stoppedCommand(command.id, waiter)
			}
			return commandOutcome{err: commandExecutionError(command.id, err)}
		}
		return commandOutcome{err: commandExecutionError(command.id, ErrOutcomeTimeout)}
	}
}

func (service *Service) failCommand(
	workContext context.Context,
	id CommandID,
	status commandStatus,
	failureCode commandFailureCode,
	outcome error,
	waiter <-chan CommandResult,
) commandOutcome {
	completedAt, err := serviceNow(service.controls.now, "Command")
	if err != nil {
		return commandOutcome{err: commandExecutionError(id, err)}
	}
	writeContext, cancelWrite := service.writeContext(workContext)
	err = service.completeCommand(writeContext, commandCompletion{
		id: id, status: status, completedAt: completedAt, failureCode: failureCode,
	})
	cancelWrite()
	if errors.Is(err, errCommandTerminal) {
		return service.awaitSatisfiedCommand(workContext, id, waiter)
	}
	if err != nil {
		if errors.Is(operationError(writeContext, err), ErrServiceStopped) {
			return service.stoppedCommand(id, waiter)
		}
		return commandOutcome{err: commandExecutionError(id, err)}
	}
	return commandOutcome{err: commandExecutionError(id, outcome)}
}

func (service *Service) awaitSatisfiedCommand(
	workContext context.Context,
	id CommandID,
	waiter <-chan CommandResult,
) commandOutcome {
	select {
	case result := <-waiter:
		return commandOutcome{result: result}
	default:
	}
	if errors.Is(context.Cause(workContext), ErrServiceStopped) {
		return service.stoppedCommand(id, waiter)
	}
	timer := time.NewTimer(service.controls.persistenceTimeout)
	defer timer.Stop()
	select {
	case result := <-waiter:
		return commandOutcome{result: result}
	case <-workContext.Done():
		return service.stoppedCommand(id, waiter)
	case <-timer.C:
		return commandOutcome{err: commandExecutionError(id, errors.New("terminal Command outcome was not delivered"))}
	}
}

func (service *Service) stoppedCommand(id CommandID, waiter <-chan CommandResult) commandOutcome {
	service.waitForObservationPublications()
	select {
	case result := <-waiter:
		return commandOutcome{result: result}
	default:
		return commandOutcome{err: commandExecutionError(id, ErrServiceStopped)}
	}
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
