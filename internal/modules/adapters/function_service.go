package adapters

import (
	"context"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

type FunctionService struct {
	repository FunctionRepository
	invoker    FunctionInvoker
	admission  *lifecycle.AdmissionGroup
}

// NewFunctionService recovers interrupted evidence before opening admission.
func NewFunctionService(
	ctx context.Context,
	repository FunctionRepository,
	invoker FunctionInvoker,
) (*FunctionService, error) {
	if repository == nil || invoker == nil {
		return nil, ErrInvalidFunctionInput
	}
	if err := repository.Recover(ctx, time.Now().UTC()); err != nil {
		return nil, errors.Join(ErrFunctionPersistence, err)
	}
	return &FunctionService{repository: repository, invoker: invoker, admission: lifecycle.NewAdmissionGroup()}, nil
}

func (service *FunctionService) RegisterCatalog(ctx context.Context, catalog Catalog) error {
	prepared, err := PrepareCatalog(catalog)
	if err != nil {
		return err
	}
	return service.repository.RegisterCatalog(ctx, prepared)
}

func (service *FunctionService) ListFunctions(
	ctx context.Context,
	adapterID string,
	page PageInput,
) (FunctionPage, error) {
	if ValidateAdapterID(adapterID) != nil || ValidateFunctionPage(page) != nil {
		return FunctionPage{}, ErrInvalidFunctionInput
	}
	if _, _, err := DecodeFunctionCursor(adapterID, page.Cursor); err != nil {
		return FunctionPage{}, err
	}
	return service.repository.ListFunctions(ctx, adapterID, page)
}

func (service *FunctionService) GetFunction(ctx context.Context, adapterID string, key FunctionKey) (Function, error) {
	if ValidateAdapterID(adapterID) != nil || !ValidFunctionKey(key) {
		return Function{}, ErrInvalidFunctionInput
	}
	return service.repository.GetFunction(ctx, adapterID, key)
}

func (service *FunctionService) GetInvocation(ctx context.Context, id InvocationID) (Invocation, error) {
	if _, err := ParseInvocationID(string(id)); err != nil {
		return Invocation{}, err
	}
	return service.repository.GetInvocation(ctx, id)
}

func (service *FunctionService) ListInvocations(ctx context.Context, filter InvocationFilter) (InvocationPage, error) {
	if ValidateInvocationFilter(filter) != nil {
		return InvocationPage{}, ErrInvalidFunctionInput
	}
	if _, _, err := DecodeInvocationCursor(filter); err != nil {
		return InvocationPage{}, err
	}
	return service.repository.ListInvocations(ctx, filter)
}

func (service *FunctionService) StopAdmission()      { service.admission.CloseAdmission() }
func (service *FunctionService) AdmissionOpen() bool { return service.admission.AdmissionOpen() }
func (service *FunctionService) Drain(ctx context.Context) error {
	service.StopAdmission()
	return service.admission.Wait(ctx)
}

func (service *FunctionService) Invoke(ctx context.Context, input InvokeInput) (Invocation, error) {
	reservation, ok := service.admission.TryAcquire()
	if !ok {
		return Invocation{}, ErrFunctionAdmissionClosed
	}
	defer reservation.Release()
	if err := ctx.Err(); err != nil {
		return Invocation{}, err
	}
	if err := ValidateInvokeScope(input); err != nil {
		return Invocation{}, err
	}
	function, err := service.repository.GetFunction(ctx, input.AdapterID, input.FunctionKey)
	if err != nil {
		return Invocation{}, err
	}
	prepared, err := PrepareInvocation(function, input, time.Now().UTC())
	if err != nil {
		return Invocation{}, err
	}
	admission, stopAdmission := context.WithDeadline(ctx, prepared.Deadline())
	bounded, stopBounded := context.WithTimeout(admission, FunctionPersistenceTimeout)
	invocation, err := service.repository.Admit(bounded, prepared)
	stopBounded()
	stopAdmission()
	if err != nil {
		return Invocation{}, err
	}
	// A committed invocation belongs to this service, not the caller's wait.
	owned, cancel := context.WithDeadline(context.WithoutCancel(ctx), invocation.Deadline)
	defer cancel()
	evidence, completedAt := service.dispatch(owned, invocation)
	persistence, stopPersistence := context.WithTimeout(context.WithoutCancel(ctx), FunctionPersistenceTimeout)
	defer stopPersistence()
	terminal, err := service.repository.Complete(persistence, invocation.ID, evidence, completedAt)
	if err != nil {
		return Invocation{}, &InvocationError{ID: invocation.ID, Err: errors.Join(ErrFunctionPersistence, err)}
	}
	return terminal, nil
}

func (service *FunctionService) dispatch(ctx context.Context, invocation Invocation) (Evidence, time.Time) {
	if !time.Now().Before(invocation.Deadline) {
		return NotExecuted{ReasonCode: "deadline_expired"}, time.Now().UTC()
	}
	evidence := service.invoker.Invoke(ctx, invocation)
	at := time.Now().UTC()
	if _, notExecuted := evidence.(NotExecuted); notExecuted {
		return evidence, at
	}
	if !at.Before(invocation.Deadline) {
		return Unknown{ReasonCode: "outcome_timeout"}, at
	}
	if evidence == nil {
		return Unknown{ReasonCode: "invalid_response"}, at
	}
	return evidence, at
}

func ValidateFunctionPage(page PageInput) error {
	const maximumCursorSize = 2048
	if (page.Limit != 0 && !ValidPageLimit(page.Limit)) || len(page.Cursor) > maximumCursorSize {
		return ErrInvalidFunctionInput
	}
	return nil
}

func ValidateInvocationFilter(filter InvocationFilter) error {
	if ValidateAdapterID(filter.AdapterID) != nil ||
		(filter.FunctionKey != "" && !ValidFunctionKey(filter.FunctionKey)) ||
		ValidateFunctionPage(filter.PageInput) != nil {
		return ErrInvalidFunctionInput
	}
	return nil
}
