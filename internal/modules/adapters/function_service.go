package adapters

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

type FunctionService struct {
	repository   FunctionRepository
	invoker      FunctionInvoker
	admission    *lifecycle.AdmissionGroup
	mutex        sync.Mutex
	publicClosed bool
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
	result, err := service.repository.ListFunctions(ctx, adapterID, page)
	if err != nil {
		return FunctionPage{}, err
	}
	for index := range result.Items {
		fingerprint, fingerprintErr := FingerprintDescriptor(result.Items[index].Descriptor)
		if fingerprintErr != nil {
			return FunctionPage{}, ErrFunctionPersistence
		}
		result.Items[index].ContractFingerprint = fingerprint
	}
	return result, nil
}

func (service *FunctionService) GetFunction(ctx context.Context, adapterID string, key FunctionKey) (Function, error) {
	if ValidateAdapterID(adapterID) != nil || !ValidFunctionKey(key) {
		return Function{}, ErrInvalidFunctionInput
	}
	function, err := service.repository.GetFunction(ctx, adapterID, key)
	if err != nil {
		return Function{}, err
	}
	fingerprint, err := FingerprintDescriptor(function.Descriptor)
	if err != nil {
		return Function{}, ErrFunctionPersistence
	}
	function.ContractFingerprint = fingerprint
	return function, nil
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

func (service *FunctionService) StopAdmission() { service.admission.CloseAdmission() }

// ClosePublicAdmission leaves reserved internal work available until StopAdmission.
func (service *FunctionService) ClosePublicAdmission() {
	service.mutex.Lock()
	service.publicClosed = true
	service.mutex.Unlock()
}
func (service *FunctionService) AdmissionOpen() bool {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	return !service.publicClosed && service.admission.AdmissionOpen()
}
func (service *FunctionService) Drain(ctx context.Context) error {
	service.StopAdmission()
	return service.admission.Wait(ctx)
}

func (service *FunctionService) Invoke(ctx context.Context, input InvokeInput) (Invocation, error) {
	return service.invoke(ctx, input, "", nil)
}

// InvokeReserved uses one caller-reserved identity and resolves the current
// contract once. An existing identity is a conflict, never a retry or join.
func (service *FunctionService) InvokeReserved(ctx context.Context, input ReservedInvokeInput) (Invocation, error) {
	if _, err := ParseInvocationID(string(input.InvocationID)); err != nil {
		return Invocation{}, ErrInvalidFunctionInput
	}
	return service.invoke(ctx, InvokeInput{}, input.InvocationID, &input.ContractInput)
}

func (service *FunctionService) invoke(
	ctx context.Context,
	input InvokeInput,
	id InvocationID,
	contract *ContractInput,
) (Invocation, error) {
	service.mutex.Lock()
	if contract == nil && service.publicClosed {
		service.mutex.Unlock()
		return Invocation{}, ErrFunctionAdmissionClosed
	}
	reservation, ok := service.admission.TryAcquire()
	service.mutex.Unlock()
	if !ok {
		return Invocation{}, ErrFunctionAdmissionClosed
	}
	defer reservation.Release()
	if err := ctx.Err(); err != nil {
		return Invocation{}, err
	}
	var function Function
	var err error
	if contract != nil {
		var arguments []byte
		function, arguments, err = service.resolveContractInput(ctx, *contract)
		input = InvokeInput{AdapterID: contract.AdapterID, FunctionKey: contract.FunctionKey,
			CatalogRevision: function.CatalogRevision, Arguments: arguments}
	} else {
		if err = ValidateInvokeScope(input); err != nil {
			return Invocation{}, err
		}
		function, err = service.GetFunction(ctx, input.AdapterID, input.FunctionKey)
		if err == nil {
			id, err = NewInvocationID()
		}
	}
	if err != nil {
		return Invocation{}, err
	}
	prepared, err := PrepareInvocation(function, input, id, time.Now().UTC())
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
