package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type CommandDelivery interface {
	Deliver(context.Context, string, CommandDispatch) (CommandAcceptance, error)
}

func (service *Service) ExecuteCommand(
	ctx context.Context,
	entityID EntityID,
	operationName OperationName,
	parameters CommandParameters,
) (CommandResult, error) {
	requestContext, moduleContext, release, err := service.beginWork(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()

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
		return CommandResult{}, normalizeServiceError(requestContext, err)
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
	requestedAt, err := service.now()
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
		return CommandResult{}, normalizeServiceError(requestContext, err)
	}

	workContext, cancelWork := withModuleCancellation(context.WithoutCancel(ctx), moduleContext)
	deadlineContext, cancelDeadline := service.controls.withDeadline(workContext, command.deadlineAt)
	completed := make(chan commandOutcome, 1)
	transferred = true
	go func() {
		defer release()
		defer cancelWork()
		defer cancelDeadline()
		defer service.removeCommandWaiter(command.id)
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
			return service.stoppedCommandOutcome(command.id, waiter)
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

	acceptedAt, err := service.now()
	if err != nil {
		return service.failCommand(workContext, command.id, commandStatusInternalFailure, commandFailureInternalError, err, waiter)
	}
	writeContext, cancel := context.WithTimeout(workContext, service.controls.persistenceTimeout)
	err = service.markCommandAccepted(writeContext, command.id, acceptedAt)
	cancel()
	if errors.Is(err, errCommandTerminal) {
		return service.terminalCommandOutcome(command.id, waiter)
	}
	if err != nil {
		if errors.Is(normalizeServiceError(workContext, err), ErrServiceStopped) {
			return service.stoppedCommandOutcome(command.id, waiter)
		}
		return service.failCommand(workContext, command.id, commandStatusInternalFailure, commandFailureInternalError, err, waiter)
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
			return service.stoppedCommandOutcome(command.id, waiter)
		}
		return service.failCommand(workContext, command.id, commandStatusOutcomeTimeout, commandFailureOutcomeTimeout, ErrOutcomeTimeout, waiter)
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
	completedAt, err := service.now()
	if err != nil {
		return commandOutcome{err: commandExecutionError(id, err)}
	}
	writeContext, cancel := context.WithTimeout(workContext, service.controls.persistenceTimeout)
	err = service.completeCommand(writeContext, commandCompletion{
		id: id, status: status, completedAt: completedAt, failureCode: failureCode,
	})
	cancel()
	if errors.Is(err, errCommandTerminal) {
		return service.terminalCommandOutcome(id, waiter)
	}
	if err != nil {
		if errors.Is(normalizeServiceError(workContext, err), ErrServiceStopped) {
			return service.stoppedCommandOutcome(id, waiter)
		}
		return commandOutcome{err: commandExecutionError(id, err)}
	}
	return commandOutcome{err: commandExecutionError(id, outcome)}
}

func (service *Service) stoppedCommandOutcome(id CommandID, waiter <-chan CommandResult) commandOutcome {
	select {
	case result := <-waiter:
		return commandOutcome{result: result}
	default:
	}
	lookupContext, cancel := context.WithTimeout(context.Background(), service.controls.persistenceTimeout)
	command, err := getCommand(lookupContext, service.database, id)
	cancel()
	if err == nil && command.status == commandStatusSatisfied {
		return service.waitForTerminalNotification(id, waiter)
	}
	return commandOutcome{err: commandExecutionError(id, ErrServiceStopped)}
}

func (service *Service) terminalCommandOutcome(id CommandID, waiter <-chan CommandResult) commandOutcome {
	select {
	case result := <-waiter:
		return commandOutcome{result: result}
	default:
	}
	lookupContext, cancel := context.WithTimeout(context.Background(), service.controls.persistenceTimeout)
	command, err := getCommand(lookupContext, service.database, id)
	cancel()
	if err != nil {
		return commandOutcome{err: commandExecutionError(id, err)}
	}
	if command.status == commandStatusSatisfied {
		return service.waitForTerminalNotification(id, waiter)
	}
	return commandOutcome{err: commandExecutionError(id, commandFailure(command))}
}

func (service *Service) waitForTerminalNotification(id CommandID, waiter <-chan CommandResult) commandOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), service.controls.persistenceTimeout)
	defer cancel()
	select {
	case result := <-waiter:
		return commandOutcome{result: result}
	case <-ctx.Done():
		return commandOutcome{err: commandExecutionError(id, errors.New("terminal command outcome was not delivered"))}
	}
}

func commandFailure(command commandRecord) error {
	switch command.status {
	case commandStatusRejected:
		return ErrUpstreamRejected
	case commandStatusAdapterUnavailable:
		return ErrAdapterUnavailable
	case commandStatusOutcomeTimeout:
		return ErrOutcomeTimeout
	case commandStatusInterrupted:
		return ErrServiceStopped
	default:
		return errors.New("command failed internally")
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
