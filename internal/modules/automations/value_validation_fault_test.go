//nolint:testpackage // Exercises private executor classification with narrow destination validation doubles.
package automations

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

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
