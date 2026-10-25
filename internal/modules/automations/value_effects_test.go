//nolint:testpackage // Deciding checks target private effect gates and committed-input reconciliation.
package automations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type flowEffectRepository struct {
	flowRepository

	startErr      error
	completionErr error
	inputs        []RunDataRecord
	completions   []FunctionCompletion
}

func (repo *flowEffectRepository) MarkStepRunning(_ context.Context, start StepStart) error {
	if repo.startErr != nil {
		return repo.startErr
	}
	repo.inputs = append(repo.inputs, start.Inputs)
	return nil
}
func (repo *flowEffectRepository) RecordNotificationStart(_ context.Context, start NotificationStart) error {
	if repo.startErr != nil {
		return repo.startErr
	}
	repo.inputs = append(repo.inputs, start.Inputs)
	return nil
}
func (repo *flowEffectRepository) RecordFunctionStart(_ context.Context, start FunctionStart) error {
	if repo.startErr != nil {
		return repo.startErr
	}
	repo.inputs = append(repo.inputs, start.Inputs)
	return nil
}
func (repo *flowEffectRepository) CompleteFunction(_ context.Context, completion FunctionCompletion) error {
	repo.completions = append(repo.completions, completion)
	return repo.completionErr
}
func (*flowEffectRepository) CompleteStep(context.Context, StepCompletion) error { return nil }
func (*flowEffectRepository) CompleteNotification(context.Context, NotificationCompletion) error {
	return nil
}
func (*flowEffectRepository) CompleteRun(context.Context, RunCompletion) error { return nil }

type flowCommandDevices struct {
	flowDevices

	inputs []devices.CommandInput
	record devices.CommandRecord
}

func (*flowCommandDevices) ValidateCommand(
	_ context.Context,
	input devices.CommandInput,
) (devices.CommandParameters, error) {
	return append(devices.CommandParameters(nil), input.Parameters...), nil
}

func (device *flowCommandDevices) ExecuteCommand(
	_ context.Context,
	input devices.CommandInput,
) (devices.CommandResult, error) {
	device.inputs = append(device.inputs, input)
	at := time.Now().UTC()
	device.record = devices.CommandRecord{
		ID:            input.ID,
		CorrelationID: input.CorrelationID,
		EntityID:      input.EntityID,
		OperationName: input.OperationName,
		Parameters:    input.Parameters,
		Status:        devices.CommandStatusDispatched,
		CompletedAt:   &at,
	}
	return devices.CommandResult{}, nil
}
func (device *flowCommandDevices) GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error) {
	return device.record, nil
}

type flowNotificationService struct {
	AutomationNotifications

	inputs []notifications.SendInput
}

func (service *flowNotificationService) Send(
	_ context.Context,
	input notifications.SendInput,
) (notifications.Attempt, error) {
	service.inputs = append(service.inputs, input)
	return notifications.Attempt{}, nil
}

func (service *flowNotificationService) GetAttempt(
	_ context.Context,
	id notifications.AttemptID,
) (notifications.Attempt, error) {
	at := time.Now().UTC()
	input := service.inputs[len(service.inputs)-1]
	return notifications.Attempt{
		ID:            id,
		DestinationID: input.DestinationID,
		Message:       input.Message,
		Outcome:       notifications.Accepted,
		CompletedAt:   &at,
	}, nil
}

type flowFunctionService struct {
	AutomationFunctions

	descriptor adapters.Descriptor
	result     json.RawMessage
	inputs     []adapters.ReservedInvokeInput
	invocation adapters.Invocation
	change     func(*adapters.Invocation)
}

func (service *flowFunctionService) ValidateContractInput(
	_ context.Context,
	input adapters.ContractInput,
) (json.RawMessage, error) {
	_, schema, _, err := adapters.PrepareDescriptor(service.descriptor)
	if err != nil {
		return nil, err
	}
	return schema.Normalize(input.Arguments)
}

func (service *flowFunctionService) InvokeReserved(
	_ context.Context,
	input adapters.ReservedInvokeInput,
) (adapters.Invocation, error) {
	service.inputs = append(service.inputs, input)
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	service.invocation = adapters.Invocation{
		ID:              input.InvocationID,
		CatalogRevision: "afc_01980000-0000-7000-8000-000000000001",
		Function:        service.descriptor,
		Route: adapters.RouteSnapshot{
			AdapterID: input.AdapterID,
			RuntimeID: "run_01980000-0000-7000-8000-000000000001",
		},
		Arguments:  append(json.RawMessage(nil), input.Arguments...),
		StartedAt:  at,
		Deadline:   at.Add(time.Second),
		Completion: &adapters.Completion{At: at, Evidence: adapters.Succeeded{Result: service.result}},
	}
	return service.invocation, nil
}
func (service *flowFunctionService) GetInvocation(context.Context, adapters.InvocationID) (adapters.Invocation, error) {
	value := service.invocation
	if service.change != nil {
		service.change(&value)
	}
	return value, nil
}

//nolint:gocognit,gocyclo,cyclop,nestif // The full injected gate matrix keeps no-effect and no-advancement assertions together.
func TestDynamicEffectsReserveExactInputsAndFunctionOutputBeforeAdvancement(t *testing.T) {
	t.Parallel()
	descriptor := adapters.Descriptor{
		Key:          "forecast",
		Description:  "Synthetic forecast",
		TimeoutMS:    1000,
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
	fingerprint, err := adapters.FingerprintDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	definition := flowDefinition(t, map[VariableName]ValueBinding{"seed": flowLiteral(`9007199254740993`)}, []Step{
		{
			ID: "forecast",
			Body: FunctionStep{
				AdapterID:           "weather",
				FunctionKey:         descriptor.Key,
				ContractFingerprint: fingerprint,
				Arguments:           flowExpression(`{seed: vars.seed}`),
			},
		},
		{
			ID: "target",
			Body: SetVariablesStep{
				Assignments: map[VariableName]ValueBinding{"target": flowExpression(`steps.forecast.output.target`)},
			},
		},
		{
			ID: "command",
			Body: CommandStep{
				EntityID:      "ent_01980000-0000-7000-8000-000000000001",
				OperationName: "set",
				Parameters:    flowExpression(`{value: vars.target}`),
			},
		},
		{
			ID: "notify",
			Body: NotificationStep{
				DestinationID: "ndst_01980000-0000-7000-8000-000000000001",
				Title:         flowExpression(`"Target " + string(vars.target)`),
				Message:       flowLiteral(`"Resolved"`),
			},
		},
	})
	run := flowRun(definition)
	run.Steps = []StepAttempt{{StepID: "command", Position: 0, State: NotAttemptedStep{}}}
	for _, fault := range []string{"none", "function start", "command start", "notification start", "output commit", "unsupported output", "output container limit", "wrong ownership", "unknown outcome"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			repo := &flowEffectRepository{}
			device := &flowCommandDevices{}
			functions := &flowFunctionService{
				descriptor: descriptor,
				result:     json.RawMessage(`{"target":9007199254740993}`),
			}
			notifier := &flowNotificationService{}
			if fault == "function start" {
				repo.startErr = errors.New("private persistence text")
			}
			if fault == "output commit" {
				repo.completionErr = errors.New("private output persistence")
			}
			if fault == "unsupported output" {
				functions.result = json.RawMessage(`{"target":9223372036854775808}`)
			}
			if fault == "output container limit" {
				functions.result = json.RawMessage(`{"items":[` + strings.Repeat("0,", 128) + `0]}`)
			}
			if fault == "wrong ownership" {
				functions.change = func(value *adapters.Invocation) { value.Arguments = json.RawMessage(`{"seed":1}`) }
			}
			if fault == "unknown outcome" {
				functions.change = func(value *adapters.Invocation) {
					completion := *value.Completion
					completion.Evidence = adapters.Unknown{ReasonCode: "reply_lost"}
					value.Completion = &completion
				}
			}
			service := NewService(repo, device, Dependencies{Functions: functions, Notifications: notifier})
			values, _ := newRunValues(run)
			if !service.initializeValues(t.Context(), run, values) {
				t.Fatal("initialization failed")
			}
			ok := service.executeFunction(
				t.Context(),
				run,
				"forecast",
				definition.Steps[0].Body.(FunctionStep),
				0,
				values,
			)
			if fault != "none" && fault != "command start" && fault != "notification start" {
				if ok || len(values.env.Steps) != 0 {
					t.Fatalf("Function fault advanced context: %s", fault)
				}
				if fault == "function start" && len(functions.inputs) != 0 {
					t.Fatal("invoked after failed input commit")
				}
				if fault == "unsupported output" || fault == "output container limit" {
					completion := repo.completions[0]
					if _, success := completion.State.(FunctionSucceeded); !success {
						t.Fatal("provider success was falsified")
					}
					want := "expression_value_invalid"
					if fault == "output container limit" {
						want = "expression_limit"
					}
					if completion.Data.Body.(RunDataFailed).FailureCode != want {
						t.Fatal("output failure missing")
					}
				}
				return
			}
			if !ok || len(functions.inputs) != 1 ||
				string(resolvedField(repo.inputs[0], "arguments")) != `{"seed":9007199254740993}` {
				t.Fatal("resolved Function inputs were not committed exactly")
			}
			if repo.completions[0].Data == nil ||
				string(repo.completions[0].Data.Body.(FunctionOutputCaptured).Output) != `{"target":9007199254740993}` {
				t.Fatal("copied output missing")
			}
			// Mutation of the provider's retained buffer cannot change the live output.
			functions.result[0] = ' '
			if !service.executeAssignment(
				t.Context(),
				run,
				values,
				"target",
				definition.Steps[1].Body.(SetVariablesStep),
			) {
				t.Fatal("output did not feed assignment")
			}
			if values.env.Vars["target"] != int64(9007199254740993) {
				t.Fatalf("output rounded: %#v", values.env.Vars)
			}
			if fault == "command start" {
				repo.startErr = errors.New("Command start failed")
			}
			ok = service.executeCommand(t.Context(), run, "command", definition.Steps[2].Body.(CommandStep), 0, values)
			if fault == "command start" {
				if ok || len(device.inputs) != 0 {
					t.Fatal("dispatched after failed Command reservation")
				}
				return
			}
			if !ok || len(device.inputs) != 1 || string(device.inputs[0].Parameters) != `{"value":9007199254740993}` {
				t.Fatal("dynamic Command input changed")
			}
			if fault == "notification start" {
				repo.startErr = errors.New("notification start failed")
			}
			ok = service.executeNotification(
				t.Context(),
				run,
				"notify",
				definition.Steps[3].Body.(NotificationStep),
				0,
				values,
			)
			if fault == "notification start" {
				if ok || len(notifier.inputs) != 0 {
					t.Fatal("sent after failed notification reservation")
				}
				return
			}
			if !ok || len(notifier.inputs) != 1 || notifier.inputs[0].Message.Title != "Target 9007199254740993" {
				t.Fatal("dynamic notification changed")
			}
		})
	}
}

type flowReferenceFunctions struct {
	AutomationFunctions

	references   int
	literals     int
	referenceErr error
}

func (functions *flowReferenceFunctions) ValidateContractReference(context.Context, adapters.ContractReference) error {
	functions.references++
	return functions.referenceErr
}

func (functions *flowReferenceFunctions) ValidateContractInput(
	_ context.Context,
	input adapters.ContractInput,
) (json.RawMessage, error) {
	functions.literals++
	return input.Arguments, nil
}

func TestFunctionSaveValidatesStaticReferenceWithoutDummyArguments(t *testing.T) {
	t.Parallel()
	descriptor := adapters.Descriptor{
		Key:          "inspect",
		Description:  "Synthetic",
		TimeoutMS:    1000,
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
	fingerprint, err := adapters.FingerprintDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	literal := FunctionStep{
		AdapterID:           "provider",
		FunctionKey:         descriptor.Key,
		ContractFingerprint: fingerprint,
		Arguments:           flowLiteral(`{"literal":9007199254740993,"expr":"not code"}`),
	}
	dynamic := literal
	dynamic.Arguments = flowExpression(`{value: vars.target}`)
	definition := flowDefinition(
		t,
		nil,
		[]Step{
			{
				ID: "route",
				Body: IfStep{
					Conditions: Condition{ID: "true", Body: ExpressionCondition{Value: flowLiteral(`true`)}},
					Then:       []Step{{ID: "literal", Body: literal}},
					Else:       []Step{{ID: "dynamic", Body: dynamic}},
				},
			},
		},
	)
	functions := &flowReferenceFunctions{}
	if _, err = validateDefinition(t.Context(), &flowReferenceDevices{}, nil, functions, definition); err != nil {
		t.Fatal(err)
	}
	if functions.references != 1 || functions.literals != 1 {
		t.Fatalf("validation split: %+v", functions)
	}
	functions.referenceErr = adapters.ErrFunctionContractChanged
	_, err = validateDefinition(t.Context(), &flowReferenceDevices{}, nil, functions, definition)
	if reference, ok := errors.AsType[*FunctionReferenceError](
		err,
	); !ok ||
		reference.ReasonCode != "function_contract_changed" {
		t.Fatalf("bad dynamic fingerprint accepted: %v", err)
	}
}
