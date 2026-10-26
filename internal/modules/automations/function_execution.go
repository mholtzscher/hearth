package automations

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

// executeFunction records the reserved link before any Adapter-owned external work.
func (service *Service) executeFunction(
	ctx context.Context,
	run Run,
	stepID StepID,
	step FunctionStep,
) bool {
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, stepID, service.executionStopReason())
		return false
	}
	id, err := service.dependencies.NewInvocationID()
	if err != nil || service.dependencies.Functions == nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	startedAt := service.dependencies.Now().UTC()
	reserved, err := service.reserveFunction(ctx, run.ID, stepID, id, startedAt)
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
		InvocationID: id, ContractInput: step.contractInput(),
	})
	state, established := service.reconcileFunction(ctx, step, id, invokeErr)
	if !established {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	at, stateErr := FunctionStateCompletion(state)
	if stateErr != nil || at == nil {
		cancel()
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	err = service.repository.CompleteStep(
		writeContext,
		StepExecutionCompletion{
			RunID:    run.ID,
			StepID:   stepID,
			Evidence: FunctionFinished{InvocationID: id, State: state},
		},
	)
	cancel()
	if err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	_, succeeded := state.(FunctionSucceeded)
	return succeeded
}

func (service *Service) reserveFunction(
	ctx context.Context,
	runID RunID,
	stepID StepID,
	invocationID adapters.InvocationID,
	startedAt time.Time,
) (bool, error) {
	service.functionReservationMutex.Lock()
	defer service.functionReservationMutex.Unlock()
	if !service.executionOpen() {
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
			Evidence: FunctionStarted{StartedAt: startedAt, InvocationID: invocationID},
		},
	)
	return err == nil, err
}

func (service *Service) reconcileFunction(
	ctx context.Context,
	step FunctionStep,
	invocationID adapters.InvocationID,
	invokeErr error,
) (FunctionTerminalState, bool) {
	// A conflicting identity must never adopt another call's evidence.
	if errors.Is(invokeErr, adapters.ErrInvocationIDConflict) {
		return nil, false
	}
	readContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	invocation, readErr := service.dependencies.Functions.GetInvocation(readContext, invocationID)
	if reason := functionAdmissionFailure(invokeErr); reason != "" {
		if !errors.Is(readErr, adapters.ErrInvocationNotFound) {
			return nil, false
		}
		state := FunctionFailed{CompletedAt: service.dependencies.Now().UTC(),
			Evidence: FunctionAdmissionRejected{ReasonCode: reason}}
		return state, true
	}
	if readErr != nil || !matchesFunctionInvocation(step, invocationID, invocation) {
		return nil, false
	}
	if errors.Is(invokeErr, adapters.ErrFunctionPersistence) {
		return nil, false
	}
	at := invocation.Completion.At
	switch outcome := invocation.Completion.Evidence.(type) {
	case adapters.Succeeded:
		return FunctionSucceeded{CompletedAt: at, CatalogRevision: invocation.CatalogRevision}, true
	case adapters.Failed, adapters.NotExecuted, adapters.Unknown:
		return FunctionFailed{CompletedAt: at, Evidence: FunctionInvocationFailed{
			CatalogRevision: invocation.CatalogRevision, Outcome: outcome}}, true
	default:
		return nil, false
	}
}

func matchesFunctionInvocation(
	step FunctionStep,
	invocationID adapters.InvocationID,
	invocation adapters.Invocation,
) bool {
	if invocation.ID != invocationID || invocation.Route.AdapterID != step.AdapterID ||
		invocation.Function.Key != step.FunctionKey || !bytes.Equal(invocation.Arguments, step.Arguments) ||
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
