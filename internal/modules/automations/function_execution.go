package automations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

// executeFunction records the reserved link before any Adapter-owned external work.
func (service *Service) executeFunction(
	ctx context.Context,
	run Run,
	stepID StepID,
	step FunctionStep,
	values *runValues,
) bool {
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, stepID, service.executionStopReason())
		return false
	}
	raw, err := values.resolvePayload(ctx, "arguments", step.Arguments)
	if err != nil {
		return service.failValues(ctx, run, stepID, StepKindFunction, "arguments", err, nil)
	}
	if service.dependencies.Functions == nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	input := step.contractInput()
	input.Arguments = raw
	arguments, err := service.dependencies.Functions.ValidateContractInput(ctx, input)
	if err != nil {
		if errors.Is(err, adapters.ErrInvalidFunctionInput) || errors.Is(err, adapters.ErrAdapterNotFound) ||
			errors.Is(err, adapters.ErrFunctionNotFound) || errors.Is(err, adapters.ErrFunctionContractChanged) {
			return service.failValues(ctx, run, stepID, StepKindFunction, "arguments", err, nil)
		}
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	input.Arguments, err = freezeResolvedObject(arguments)
	if err != nil {
		return service.failValues(ctx, run, stepID, StepKindFunction, "arguments", err, nil)
	}
	id, err := service.dependencies.NewInvocationID()
	if err != nil || service.dependencies.Functions == nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	startedAt := service.dependencies.Now().UTC()
	reserved, err := service.reserveFunction(ctx, run.ID, stepID, id, startedAt, input.Arguments)
	if err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	if !reserved {
		service.interruptBranchRun(ctx, run.ID, stepID, service.executionStopReason())
		return false
	}
	// The persisted reservation remains admitted even when public gates close.
	_, invokeErr := service.dependencies.Functions.InvokeReserved(ctx, adapters.ReservedInvokeInput{
		InvocationID: id, ContractInput: input,
	})
	state, result, established := service.reconcileFunction(ctx, step, input.Arguments, id, invokeErr)
	if !established {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	return service.completeFunctionValueFlow(ctx, run, stepID, id, state, result, values)
}

// completeFunctionValueFlow consumes established provider evidence; it cannot
// invoke or reconcile the provider.
func (service *Service) completeFunctionValueFlow(
	ctx context.Context,
	run Run,
	stepID StepID,
	id adapters.InvocationID,
	state FunctionTerminalState,
	result json.RawMessage,
	values *runValues,
) bool {
	var output json.RawMessage
	var failure *ValueFailure
	var next exprvalue.Environment
	if _, succeeded := state.(FunctionSucceeded); succeeded {
		value, valueErr := exprvalue.DecodeValue(result)
		if valueErr == nil {
			next, valueErr = values.withOutput(stepID, value)
		}
		if valueErr != nil {
			f := valueFailure("", valueErr, nil)
			failure = &f
		} else {
			output = value.JSON
		}
	}
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	at, stateErr := FunctionStateCompletion(state)
	if stateErr != nil || at == nil {
		cancel()
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	err := service.repository.CompleteStep(
		writeContext,
		StepExecutionCompletion{
			RunID:    run.ID,
			StepID:   stepID,
			Evidence: FunctionFinished{InvocationID: id, State: state, Output: output, ValueFailure: failure},
		},
	)
	cancel()
	if err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	_, succeeded := state.(FunctionSucceeded)
	if succeeded && failure == nil {
		values.env = next
		return true
	}
	return false
}

func (service *Service) reserveFunction(
	ctx context.Context,
	runID RunID,
	stepID StepID,
	invocationID adapters.InvocationID,
	startedAt time.Time,
	arguments json.RawMessage,
) (bool, error) {
	service.functionReservationMutex.Lock()
	defer service.functionReservationMutex.Unlock()
	if !service.executionOpen() || ctx.Err() != nil {
		return false, nil
	}
	// StopAdmission cannot close execution between this check and the commit.
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	err := service.repository.RecordStep(
		writeContext,
		StepExecutionRecord{
			RunID:    runID,
			StepID:   stepID,
			Evidence: FunctionStarted{StartedAt: startedAt, InvocationID: invocationID, Arguments: arguments},
		},
	)
	return err == nil, err
}

func (service *Service) reconcileFunction(
	ctx context.Context,
	step FunctionStep,
	arguments json.RawMessage,
	invocationID adapters.InvocationID,
	invokeErr error,
) (FunctionTerminalState, json.RawMessage, bool) {
	// A conflicting identity must never adopt another call's evidence.
	if errors.Is(invokeErr, adapters.ErrInvocationIDConflict) {
		return nil, nil, false
	}
	readContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	invocation, readErr := service.dependencies.Functions.GetInvocation(readContext, invocationID)
	if reason := functionAdmissionFailure(invokeErr); reason != "" {
		if !errors.Is(readErr, adapters.ErrInvocationNotFound) {
			return nil, nil, false
		}
		state := FunctionFailed{CompletedAt: service.dependencies.Now().UTC(),
			Evidence: FunctionAdmissionRejected{ReasonCode: reason}}
		return state, nil, true
	}
	if readErr != nil || !matchesFunctionInvocation(step, arguments, invocationID, invocation) {
		return nil, nil, false
	}
	if errors.Is(invokeErr, adapters.ErrFunctionPersistence) {
		return nil, nil, false
	}
	at := invocation.Completion.At
	switch outcome := invocation.Completion.Evidence.(type) {
	case adapters.Succeeded:
		return FunctionSucceeded{
			CompletedAt:     at,
			CatalogRevision: invocation.CatalogRevision,
		}, append(
			json.RawMessage(nil),
			outcome.Result...), true
	case adapters.Failed, adapters.NotExecuted, adapters.Unknown:
		return FunctionFailed{CompletedAt: at, Evidence: FunctionInvocationFailed{
			CatalogRevision: invocation.CatalogRevision, Outcome: outcome}}, nil, true
	default:
		return nil, nil, false
	}
}

func matchesFunctionInvocation(
	step FunctionStep,
	arguments json.RawMessage,
	invocationID adapters.InvocationID,
	invocation adapters.Invocation,
) bool {
	if invocation.ID != invocationID || invocation.Route.AdapterID != step.AdapterID ||
		invocation.Function.Key != step.FunctionKey || !bytes.Equal(invocation.Arguments, arguments) ||
		invocation.Completion == nil ||
		!adapters.ValidFunctionTime(
			invocation.StartedAt,
		) || !functionCatalogPattern.MatchString(string(invocation.CatalogRevision)) {
		return false
	}
	fingerprint, err := adapters.FingerprintDescriptor(invocation.Function)
	if err != nil || fingerprint != step.ContractFingerprint {
		return false
	}
	_, _, output, err := adapters.PrepareDescriptor(invocation.Function)
	if err != nil ||
		!invocation.Deadline.Equal(
			invocation.StartedAt.Add(time.Duration(invocation.Function.TimeoutMS)*time.Millisecond),
		) {
		return false
	}
	_, err = adapters.PrepareFunctionEvidence(invocation.Completion.Evidence, output,
		invocation.Deadline, invocation.Completion.At)
	return err == nil
}

func functionAdmissionFailure(err error) string {
	switch {
	case errors.Is(err, adapters.ErrFunctionPersistence):
		return ""
	case errors.Is(err, adapters.ErrAdapterNotFound):
		return "adapter_not_found"
	case errors.Is(err, adapters.ErrFunctionNotFound):
		return "function_not_found"
	case errors.Is(err, adapters.ErrFunctionContractChanged):
		return "function_contract_changed"
	case errors.Is(err, adapters.ErrCatalogChanged):
		return "catalog_changed"
	case errors.Is(err, adapters.ErrAdapterUnavailable):
		return "adapter_unavailable"
	case errors.Is(err, adapters.ErrInvalidFunctionInput):
		return "invalid_function_arguments"
	case errors.Is(err, adapters.ErrFunctionAdmissionClosed):
		return "function_admission_closed"
	default:
		return ""
	}
}
