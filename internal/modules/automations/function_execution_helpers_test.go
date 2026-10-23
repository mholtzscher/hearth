package automations_test

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type scriptedFunctions struct {
	outcome       adapters.Evidence
	validationErr error
	invokeErr     error
	readErr       error
	changeRead    func(*adapters.Invocation)
	onInvoke      func(adapters.ReservedInvokeInput)
	inputs        []adapters.ReservedInvokeInput
	invocation    adapters.Invocation
}

func (stub *scriptedFunctions) ValidateContractInput(
	_ context.Context,
	input adapters.ContractInput,
) (json.RawMessage, error) {
	if stub.validationErr != nil {
		return nil, stub.validationErr
	}
	prepared, schema, _, err := adapters.PrepareDescriptor(functionDescriptor())
	if err != nil {
		return nil, err
	}
	fingerprint, err := adapters.FingerprintDescriptor(prepared)
	if err != nil {
		return nil, err
	}
	if input.FunctionKey != prepared.Key {
		return nil, adapters.ErrFunctionNotFound
	}
	if input.ContractFingerprint != fingerprint {
		return nil, adapters.ErrFunctionContractChanged
	}
	arguments, err := schema.Normalize(input.Arguments)
	if err != nil {
		return nil, adapters.ErrInvalidFunctionInput
	}
	return arguments, nil
}

func (stub *scriptedFunctions) InvokeReserved(
	_ context.Context,
	input adapters.ReservedInvokeInput,
) (adapters.Invocation, error) {
	stub.inputs = append(stub.inputs, input)
	if stub.onInvoke != nil {
		stub.onInvoke(input)
	}
	if stub.invokeErr != nil {
		return adapters.Invocation{}, stub.invokeErr
	}
	arguments, err := functionschema.NormalizeObject(input.Arguments)
	if err != nil {
		return adapters.Invocation{}, err
	}
	start := runtimeTestNow
	stub.invocation = adapters.Invocation{
		ID:              input.InvocationID,
		CatalogRevision: "afc_01980000-0000-7000-8000-000000000001",
		Function:        functionDescriptor(),
		Route: adapters.RouteSnapshot{
			AdapterID: input.AdapterID,
			RuntimeID: "run_01980000-0000-7000-8000-000000000001",
		},
		Arguments:  arguments,
		StartedAt:  start,
		Deadline:   start.Add(time.Second),
		Completion: &adapters.Completion{At: start, Evidence: stub.outcome},
	}
	return stub.invocation, nil
}

func (stub *scriptedFunctions) GetInvocation(_ context.Context, id adapters.InvocationID) (adapters.Invocation, error) {
	if stub.readErr != nil {
		return adapters.Invocation{}, stub.readErr
	}
	if stub.invocation.ID != id {
		return adapters.Invocation{}, adapters.ErrInvocationNotFound
	}
	value := stub.invocation
	if stub.changeRead != nil {
		stub.changeRead(&value)
	}
	return value, nil
}
