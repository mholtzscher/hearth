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
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type flowEffectRepository struct {
	flowRepository
	startErr        error
	completionErr   error
	completionLimit bool
	inputs          []StepExecutionRecord
	completions     []FunctionFinished
}

func (repo *flowEffectRepository) RecordStep(ctx context.Context, record StepExecutionRecord) error {
	effect := false
	switch record.Evidence.(type) {
	case CommandStarted, NotificationStarted, FunctionStarted:
		if repo.startErr != nil {
			return repo.startErr
		}
		effect = true
	}
	if err := repo.flowRepository.RecordStep(ctx, record); err != nil {
		return err
	}
	if effect {
		repo.inputs = append(repo.inputs, record)
	}
	return nil
}
func (repo *flowEffectRepository) CompleteStep(_ context.Context, completion StepExecutionCompletion) error {
	if repo.completionErr != nil {
		return repo.completionErr
	}
	if evidence, ok := completion.Evidence.(FunctionFinished); ok && repo.completionLimit &&
		evidence.ValueFailure == nil {
		return &exprvalue.Failure{Code: "run_data_limit"}
	}
	found := false
	for _, record := range repo.inputs {
		if record.StepID == completion.StepID {
			found = true
			details, _, err := PrepareStepExecutionDetails(record)
			if err != nil {
				return err
			}
			if _, _, err = CompleteStepDetails(details, completion); err != nil {
				return err
			}
			break
		}
	}
	if !found {
		return errors.New("completion without reservation")
	}
	if evidence, ok := completion.Evidence.(FunctionFinished); ok {
		repo.completions = append(repo.completions, evidence)
	}
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

func TestCommandReservationRetainsResolvedInputsBeforeDispatch(t *testing.T) {
	t.Parallel()
	definition := flowDefinition(t, map[VariableName]ValueBinding{"target": flowLiteral(`9007199254740993`)}, []Step{{
		ID: "command", Body: CommandStep{EntityID: "ent_01980000-0000-7000-8000-000000000001",
			OperationName: "set", Parameters: flowExpression(`{value: vars.target}`)},
	}})
	for _, reject := range []bool{false, true} {
		run := flowRun(definition)
		repo := &flowEffectRepository{}
		device := &flowCommandDevices{}
		service := NewService(repo, device, Dependencies{})
		values, err := newRunValues(run)
		if err != nil || !service.initializeValues(t.Context(), run, values) {
			t.Fatalf("initialization: %v", err)
		}
		if reject {
			repo.startErr = errors.New("reservation commit failed")
		}
		ok := service.executeCommand(t.Context(), run, "command", definition.Steps[0].Body.(CommandStep), values)
		if reject {
			if ok || len(device.inputs) != 0 || len(repo.inputs) != 0 || repo.interrupts != 1 {
				t.Fatal("failed reservation dispatched or omitted interruption")
			}
			continue
		}
		if !ok || len(device.inputs) != 1 || len(repo.inputs) != 1 ||
			string(resolvedInputField(repo.inputs[0].Inputs, "parameters")) != `{"value":9007199254740993}` ||
			string(device.inputs[0].Parameters) != `{"value":9007199254740993}` ||
			device.inputs[0].EntityID != "ent_01980000-0000-7000-8000-000000000001" || device.inputs[0].OperationName != "set" {
			t.Fatal("reservation, dispatch or fixed target changed")
		}
	}
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

func TestFunctionOutputAndOwnershipFailuresStopValueFlow(t *testing.T) {
	t.Parallel()
	descriptor := adapters.Descriptor{Key: "forecast", Description: "Synthetic forecast", TimeoutMS: 1000,
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}
	fingerprint, err := adapters.FingerprintDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	definition := flowDefinition(t, map[VariableName]ValueBinding{"seed": flowLiteral(`9007199254740993`)}, []Step{{
		ID: "forecast", Body: FunctionStep{AdapterID: "weather", FunctionKey: descriptor.Key,
			ContractFingerprint: fingerprint, Arguments: flowExpression(`{seed: vars.seed}`)},
	}})
	for _, fault := range []string{"none", "ownership", "invalid output", "completion", "capacity"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			run := flowRun(definition)
			repo := &flowEffectRepository{}
			functions := &flowFunctionService{
				descriptor: descriptor,
				result:     json.RawMessage(`{"target":9007199254740993}`),
			}
			switch fault {
			case "ownership":
				functions.change = func(i *adapters.Invocation) { i.Arguments = json.RawMessage(`{"seed":1}`) }
			case "invalid output":
				functions.result = json.RawMessage(`{"target":9223372036854775808}`)
			case "completion":
				repo.completionErr = errors.New("failed completion")
			case "capacity":
				repo.completionLimit = true
			}
			service := NewService(repo, &flowDevices{}, Dependencies{Functions: functions})
			values, err := newRunValues(run)
			if err != nil || !service.initializeValues(t.Context(), run, values) {
				t.Fatalf("initialization: %v", err)
			}
			ok := service.executeFunction(t.Context(), run, "forecast", definition.Steps[0].Body.(FunctionStep), values)
			if ok != (fault == "none") || len(functions.inputs) != 1 || len(repo.inputs) != 1 {
				t.Fatalf("fault=%s ok=%t calls=%d reservations=%d", fault, ok, len(functions.inputs), len(repo.inputs))
			}
			if string(resolvedInputField(repo.inputs[0].Inputs, "arguments")) != `{"seed":9007199254740993}` ||
				functions.inputs[0].AdapterID != "weather" || functions.inputs[0].FunctionKey != "forecast" {
				t.Fatal("committed Function inputs or fixed route changed")
			}
			if fault == "none" {
				if len(repo.completions) != 1 || string(repo.completions[0].Output) != `{"target":9007199254740993}` ||
					values.env.Steps["forecast"].(map[string]any)["output"].(map[string]any)["target"] != int64(
						9007199254740993,
					) {
					t.Fatal("verified output not copied into committed and live values")
				}
			} else if len(values.env.Steps) != 0 {
				t.Fatal("failed Function advanced output")
			}
			if fault == "invalid output" || fault == "capacity" {
				want := "expression_value_invalid"
				if fault == "capacity" {
					want = "run_data_limit"
				}
				if len(repo.completions) != 1 || repo.completions[0].ValueFailure == nil ||
					repo.completions[0].ValueFailure.FailureCode != want {
					t.Fatal("provider success did not retain value failure")
				}
				if _, ok := repo.completions[0].State.(FunctionSucceeded); !ok {
					t.Fatal("provider success was falsified")
				}
			}
		})
	}
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
				values,
			)
			if fault != "none" && fault != "command start" && fault != "notification start" {
				if ok || len(values.env.Steps) != 0 {
					t.Fatalf("Function fault advanced context: %s", fault)
				}
				if fault == "function start" && len(functions.inputs) != 0 {
					t.Fatal("invoked after failed input commit")
				}
				if len(device.inputs) != 0 || len(notifier.inputs) != 0 || len(functions.inputs) > 1 {
					t.Fatal("Function fault caused downstream work or replay")
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
					if completion.ValueFailure.FailureCode != want {
						t.Fatal("output failure missing")
					}
				}
				return
			}
			if !ok || len(functions.inputs) != 1 ||
				string(resolvedInputField(repo.inputs[0].Inputs, "arguments")) != `{"seed":9007199254740993}` {
				t.Fatal("resolved Function inputs were not committed exactly")
			}
			if len(repo.completions[0].Output) == 0 ||
				string(repo.completions[0].Output) != `{"target":9007199254740993}` {
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
			ok = service.executeCommand(t.Context(), run, "command", definition.Steps[2].Body.(CommandStep), values)
			if fault == "command start" {
				if ok || len(device.inputs) != 0 {
					t.Fatal("dispatched after failed Command reservation")
				}
				return
			}
			if !ok || len(device.inputs) != 1 || string(device.inputs[0].Parameters) != `{"value":9007199254740993}` {
				t.Fatal("dynamic Command input changed")
			}
			if len(repo.inputs) != 2 ||
				string(
					resolvedInputField(repo.inputs[1].Inputs, "parameters"),
				) != string(
					device.inputs[0].Parameters,
				) ||
				device.inputs[0].EntityID != "ent_01980000-0000-7000-8000-000000000001" ||
				device.inputs[0].OperationName != "set" {
				t.Fatal("Command reservation or fixed target changed")
			}
			if fault == "notification start" {
				repo.startErr = errors.New("notification start failed")
			}
			ok = service.executeNotification(
				t.Context(),
				run,
				"notify",
				definition.Steps[3].Body.(NotificationStep),
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
			if len(repo.inputs) != 3 ||
				string(resolvedInputField(repo.inputs[2].Inputs, "title")) != `"Target 9007199254740993"` ||
				string(resolvedInputField(repo.inputs[2].Inputs, "message")) != `"Resolved"` ||
				notifier.inputs[0].DestinationID != "ndst_01980000-0000-7000-8000-000000000001" {
				t.Fatal("Notification reservation or fixed destination changed")
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
