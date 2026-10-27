//nolint:testpackage // Exercises private executor classification with narrow destination validation doubles.
package automations

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type validationFaultDevices struct {
	flowCommandDevices

	err error
}

func (device *validationFaultDevices) ValidateCommand(
	context.Context,
	devices.CommandInput,
) (devices.CommandParameters, error) {
	return nil, device.err
}

type validationFaultFunctions struct {
	flowFunctionService

	err error
}

func (functions *validationFaultFunctions) ValidateContractInput(
	context.Context,
	adapters.ContractInput,
) (json.RawMessage, error) {
	return nil, functions.err
}

//nolint:gocognit // The cross-kind rejection/fault matrix verifies failure attribution and the absence of downstream effects.
func TestResolvedDestinationValidationSeparatesRejectionFromOperationalFault(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		kind        StepKind
		err         error
		interrupted bool
	}{
		{"Command invalid input", StepKindCommand, devices.ErrInvalidCommand, false},
		{"Command missing Entity", StepKindCommand, devices.ErrEntityNotFound, false},
		{"Command read failure", StepKindCommand, errors.New("private storage detail"), true},
		{"Command canceled read", StepKindCommand, context.Canceled, true},
		{"Function invalid input", StepKindFunction, adapters.ErrInvalidFunctionInput, false},
		{"Function missing", StepKindFunction, adapters.ErrFunctionNotFound, false},
		{"Function missing Adapter", StepKindFunction, adapters.ErrAdapterNotFound, false},
		{"Function contract changed", StepKindFunction, adapters.ErrFunctionContractChanged, false},
		{"Function read failure", StepKindFunction, adapters.ErrFunctionPersistence, true},
		{"Function unknown failure", StepKindFunction, errors.New("private catalog detail"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			step := Step{ID: "effect", Body: CommandStep{
				EntityID:      "ent_01980000-0000-7000-8000-000000000001",
				OperationName: "set",
				Parameters:    flowLiteral(`{}`),
			}}
			if test.kind == StepKindFunction {
				step.Body = FunctionStep{AdapterID: "weather", FunctionKey: "forecast", Arguments: flowLiteral(`{}`),
					ContractFingerprint: "sha256-v1:0000000000000000000000000000000000000000000000000000000000000000"}
			}
			run := flowRun(flowDefinition(t, nil, []Step{step}))
			repo := &flowEffectRepository{}
			device := &validationFaultDevices{err: test.err}
			functions := &validationFaultFunctions{err: test.err}
			service := NewService(repo, device, Dependencies{Functions: functions})
			values, err := newRunValues(run)
			if err != nil || !service.initializeValues(t.Context(), run, values) {
				t.Fatalf("initialize: %v", err)
			}
			var advanced bool
			if test.kind == StepKindCommand {
				advanced = service.executeCommand(
					t.Context(),
					run,
					"effect",
					run.Snapshot.Steps[0].Body.(CommandStep),
					values,
				)
			} else {
				advanced = service.executeFunction(
					t.Context(),
					run,
					"effect",
					run.Snapshot.Steps[0].Body.(FunctionStep),
					values,
				)
			}
			if advanced || len(repo.inputs) != 0 || len(device.inputs) != 0 || len(functions.inputs) != 0 ||
				len(values.env.Steps) != 0 {
				t.Fatal("validation failure reserved, dispatched or advanced")
			}
			if test.interrupted {
				if repo.interrupts != 1 || repo.failure != nil || len(repo.records) != 0 {
					t.Fatal("operational fault was recorded as invalid input")
				}
			} else if repo.interrupts != 0 || repo.failure == nil || repo.failure.FailureCode != "resolved_input_invalid" {
				t.Fatal("definitive input rejection was not recorded")
			}
		})
	}
}

type normalizingCommandDevices struct {
	flowCommandDevices

	validated  devices.CommandParameters
	normalized devices.CommandParameters
}

func (device *normalizingCommandDevices) ValidateCommand(
	_ context.Context,
	input devices.CommandInput,
) (devices.CommandParameters, error) {
	device.validated = append(devices.CommandParameters(nil), input.Parameters...)
	return device.normalized, nil
}

type normalizingFunctionService struct {
	flowFunctionService

	validated  json.RawMessage
	normalized json.RawMessage
}

func (functions *normalizingFunctionService) ValidateContractInput(
	_ context.Context,
	input adapters.ContractInput,
) (json.RawMessage, error) {
	functions.validated = append(json.RawMessage(nil), input.Arguments...)
	return functions.normalized, nil
}

func TestEffectEvidenceAndDispatchUseDestinationNormalizedInputs(t *testing.T) {
	t.Parallel()
	descriptor := adapters.Descriptor{Key: "forecast", Description: "Synthetic normalization", TimeoutMS: 1000,
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}
	fingerprint, err := adapters.FingerprintDescriptor(descriptor)
	require.NoError(t, err)
	definition := flowDefinition(t, nil, []Step{
		{ID: "command", Body: CommandStep{EntityID: "ent_01980000-0000-7000-8000-000000000001", OperationName: "set",
			Parameters: flowLiteral(`{"value":true}`)}},
		{ID: "function", Body: FunctionStep{AdapterID: "weather", FunctionKey: descriptor.Key,
			ContractFingerprint: fingerprint, Arguments: flowLiteral(`{"value":true}`)}},
	})
	run := flowRun(definition)
	const borrowed = ` { "value":true, "default":9007199254740993, "text":"<&>" } `
	const normalized = `{"value":true,"default":9007199254740993,"text":"\u003c\u0026\u003e"}`
	device := &normalizingCommandDevices{normalized: devices.CommandParameters(borrowed)}
	functions := &normalizingFunctionService{normalized: json.RawMessage(borrowed)}
	functions.descriptor, functions.result = descriptor, json.RawMessage(`{"ok":true}`)
	repo := &flowEffectRepository{}
	repo.snapshot = &definition
	service := NewService(repo, device, Dependencies{Functions: functions})
	values, err := newRunValues(run)
	require.NoError(t, err)
	require.True(t, service.initializeValues(t.Context(), run, values))
	require.True(t, service.executeSequence(t.Context(), run, definition.Steps, values))
	require.Len(t, repo.inputs, 2)
	require.JSONEq(t, `{"value":true}`, string(device.validated))
	require.JSONEq(t, `{"value":true}`, string(functions.validated))
	require.Len(t, device.inputs, 1)
	require.Len(t, functions.inputs, 1)
	// Validators may return borrowed buffers; retained and dispatched inputs own
	// the normalized result, never the pre-validation value or that provider buffer.
	device.normalized[0], functions.normalized[0] = '[', '['
	for _, raw := range []json.RawMessage{json.RawMessage(device.inputs[0].Parameters), functions.inputs[0].Arguments,
		repo.inputs[0].Evidence.(CommandStarted).Parameters, repo.inputs[1].Evidence.(FunctionStarted).Arguments} {
		if string(raw) != normalized {
			t.Fatalf("normalized input changed: %s", raw)
		}
	}
}
