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
	position int,
	values *runValues,
) bool {
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, stepID, service.executionStopReason())
		return false
	}
	path := stepBindingPath(run.Snapshot.Steps, stepID, "/steps") + "/arguments"
	raw, err := values.resolvePayload(ctx, step.Arguments, path)
	if err != nil {
		return service.failValues(ctx, run, values, &stepID, path, err, nil)
	}
	if service.dependencies.Functions == nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	input := step.contractInput()
	input.Arguments = raw
	arguments, err := service.dependencies.Functions.ValidateContractInput(ctx, input)
	if err != nil {
		return service.failValues(ctx, run, values, &stepID, path, err, nil)
	}
	input.Arguments = append(json.RawMessage(nil), arguments...)
	inputs, err := resolvedInputRecord(
		values,
		stepID,
		service.dependencies.Now(),
		map[string]json.RawMessage{"arguments": input.Arguments},
	)
	if err != nil {
		return service.failValues(ctx, run, values, &stepID, path, err, nil)
	}
	input.Arguments = resolvedField(inputs, "arguments")
	id, err := service.dependencies.NewInvocationID()
	if err != nil || service.dependencies.Functions == nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	start := FunctionStart{RunID: run.ID, StepID: stepID, Position: position,
		InvocationID: id, StartedAt: service.dependencies.Now().UTC(), Inputs: inputs}
	reserved, err := service.reserveFunction(ctx, start)
	if err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	if !reserved {
		service.interruptBranchRun(ctx, run.ID, stepID, service.executionStopReason())
		return false
	}
	values.committed(inputs)
	// The persisted reservation remains admitted even when public gates close.
	_, invokeErr := service.dependencies.Functions.InvokeReserved(ctx, adapters.ReservedInvokeInput{
		InvocationID: id, ContractInput: input,
	})
	completion, established := service.reconcileFunction(ctx, step, start, invokeErr)
	if !established {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	return service.completeFunctionValues(ctx, run, stepID, values, completion)
}

// completeFunctionValues commits verified provider evidence and the copied output
// atomically before publishing that output to the active Run context.
func (service *Service) completeFunctionValues(
	ctx context.Context,
	run Run,
	stepID StepID,
	values *runValues,
	completion FunctionCompletion,
) bool {
	var next exprvalue.Environment
	var outputErr error
	if _, succeeded := completion.State.(FunctionSucceeded); succeeded {
		if completion.Data == nil {
			service.interruptRunExecutorFault(ctx, run.ID, stepID)
			return false
		}
		captured, ok := completion.Data.Body.(FunctionOutputCaptured)
		if !ok {
			service.interruptRunExecutorFault(ctx, run.ID, stepID)
			return false
		}
		var data RunDataRecord
		data, next, outputErr = prepareFunctionOutput(
			values,
			stepID,
			stepBindingPath(run.Snapshot.Steps, stepID, "/steps"),
			service.dependencies.Now(),
			captured.Output,
		)
		completion.Data = &data
	}
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.CompleteFunction(writeContext, completion)
	cancel()
	if err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	if completion.Data != nil {
		values.committed(*completion.Data)
	}
	if outputErr != nil {
		return false
	}
	_, succeeded := completion.State.(FunctionSucceeded)
	if succeeded {
		values.env = next
	}
	return succeeded
}

// prepareFunctionOutput carries the typed live value and its encoded evidence
// together. An unusable output preserves provider success but fails the Run.
func prepareFunctionOutput(
	values *runValues,
	id StepID,
	path string,
	at time.Time,
	raw json.RawMessage,
) (RunDataRecord, exprvalue.Environment, error) {
	output, err := exprvalue.DecodeValue(raw)
	var next exprvalue.Environment
	if err == nil {
		next, err = values.withOutput(id, output)
	}
	record := values.record(&id, at, FunctionOutputCaptured{Output: output.JSON})
	if err == nil {
		err = values.budget(record)
	}
	if err != nil {
		record.Body = RunDataFailed{Path: path, FailureCode: dataFailureCode(err)}
	}
	return record, next, err
}

func (service *Service) reserveFunction(ctx context.Context, start FunctionStart) (bool, error) {
	service.functionReservationMutex.Lock()
	defer service.functionReservationMutex.Unlock()
	if !service.executionOpen() {
		return false, nil
	}
	// StopAdmission cannot close execution between this check and the commit.
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	err := service.repository.RecordFunctionStart(writeContext, start)
	return err == nil, err
}

func (service *Service) reconcileFunction(
	ctx context.Context,
	step FunctionStep,
	start FunctionStart,
	invokeErr error,
) (FunctionCompletion, bool) {
	completion := FunctionCompletion{RunID: start.RunID, StepID: start.StepID, InvocationID: start.InvocationID}
	// A conflicting identity must never adopt another call's evidence.
	if errors.Is(invokeErr, adapters.ErrInvocationIDConflict) {
		return completion, false
	}
	readContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	invocation, readErr := service.dependencies.Functions.GetInvocation(readContext, start.InvocationID)
	if reason := functionAdmissionFailure(invokeErr); reason != "" {
		if !errors.Is(readErr, adapters.ErrInvocationNotFound) {
			return completion, false
		}
		completion.State = FunctionFailed{CompletedAt: service.dependencies.Now().UTC(),
			Evidence: FunctionAdmissionRejected{ReasonCode: reason}}
		return completion, true
	}
	if readErr != nil || !matchesFunctionInvocation(step, start, invocation) {
		return completion, false
	}
	if errors.Is(invokeErr, adapters.ErrFunctionPersistence) {
		return completion, false
	}
	at := invocation.Completion.At
	switch outcome := invocation.Completion.Evidence.(type) {
	case adapters.Succeeded:
		completion.State = FunctionSucceeded{CompletedAt: at, CatalogRevision: invocation.CatalogRevision}
		completion.Data = &RunDataRecord{
			Body: FunctionOutputCaptured{Output: append(json.RawMessage(nil), outcome.Result...)},
		}
	case adapters.Failed, adapters.NotExecuted, adapters.Unknown:
		completion.State = FunctionFailed{CompletedAt: at, Evidence: FunctionInvocationFailed{
			CatalogRevision: invocation.CatalogRevision, Outcome: outcome}}
	default:
		return completion, false
	}
	return completion, true
}

func matchesFunctionInvocation(step FunctionStep, start FunctionStart, invocation adapters.Invocation) bool {
	if invocation.ID != start.InvocationID || invocation.Route.AdapterID != step.AdapterID ||
		invocation.Function.Key != step.FunctionKey || !bytes.Equal(invocation.Arguments, resolvedField(start.Inputs, "arguments")) ||
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
