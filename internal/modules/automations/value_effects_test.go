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
	startStep       StepID
	completionErr   error
	completionLimit bool
	inputs          []StepExecutionRecord
	completions     []FunctionFinished
}

func (repo *flowEffectRepository) RecordStep(ctx context.Context, record StepExecutionRecord) error {
	effect := false
	switch record.Evidence.(type) {
	case CommandStarted, NotificationStarted, FunctionStarted:
		if repo.startErr != nil && (repo.startStep == "" || repo.startStep == record.StepID) {
			return repo.startErr
		}
		effect = true
	case BranchDecided, CommandStopped, DelayStarted, StateRead, ValueResolutionFailed, ValuesAssigned:
		// Non-effect reaches are handled by the underlying value-flow double.
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

//nolint:gocognit,gocyclo,cyclop // One sequence-level gate matrix owns effect ordering, no replay and committed value flow.
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
	for _, fault := range []string{"none", "function start", "command start", "notification start", "output commit", "unsupported output", "output container limit", "wrong ownership", "unknown outcome", "capacity"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			repo := &flowEffectRepository{}
			device := &flowCommandDevices{}
			functions := &flowFunctionService{
				descriptor: descriptor,
				result:     json.RawMessage(`{"target":9007199254740993}`),
			}
			notifier := &flowNotificationService{}
			switch fault {
			case "function start", "command start", "notification start":
				repo.startStep = map[string]StepID{"function start": "forecast", "command start": "command", "notification start": "notify"}[fault]
				repo.startErr = errors.New("reservation commit failed")
			case "output commit":
				repo.completionErr = errors.New("private output persistence")
			case "unsupported output":
				functions.result = json.RawMessage(`{"target":9223372036854775808}`)
			case "output container limit":
				functions.result = json.RawMessage(`{"items":[` + strings.Repeat("0,", 128) + `0]}`)
			case "wrong ownership":
				functions.change = func(value *adapters.Invocation) { value.Arguments = json.RawMessage(`{"seed":1}`) }
			case "unknown outcome":
				functions.change = func(value *adapters.Invocation) {
					completion := *value.Completion
					completion.Evidence = adapters.Unknown{ReasonCode: "reply_lost"}
					value.Completion = &completion
				}
			case "capacity":
				repo.completionLimit = true
			}
			service := NewService(repo, device, Dependencies{Functions: functions, Notifications: notifier})
			values, _ := newRunValues(run)
			if !service.initializeValues(t.Context(), run, values) {
				t.Fatal("initialization failed")
			}
			ok := service.executeSequence(t.Context(), run, definition.Steps, values)
			functionCalls, commandCalls, sends, reservations := 1, 0, 0, 1
			switch fault {
			case "function start":
				functionCalls, reservations = 0, 0
			case "notification start":
				commandCalls, reservations = 1, 2
			case "none":
				commandCalls, sends, reservations = 1, 1, 3
			}
			if ok != (fault == "none") || len(functions.inputs) != functionCalls ||
				len(device.inputs) != commandCalls ||
				len(notifier.inputs) != sends ||
				len(repo.inputs) != reservations {
				t.Fatalf("gate %s: ok=%t calls=%d/%d/%d reservations=%d", fault, ok,
					len(functions.inputs), len(device.inputs), len(notifier.inputs), len(repo.inputs))
			}
			if functionCalls == 1 &&
				(functions.inputs[0].AdapterID != "weather" || functions.inputs[0].FunctionKey != "forecast" ||
					string(repo.inputs[0].Evidence.(FunctionStarted).Arguments) != `{"seed":9007199254740993}`) {
				t.Fatal("Function input or static route changed")
			}
			if repo.startErr != nil && repo.interrupts != 1 {
				t.Fatal("failed reservation omitted interruption")
			}
			advanced := fault == "none" || fault == "command start" || fault == "notification start"
			if advanced {
				functions.result[0] = ' ' // Retained provider buffers cannot change owned output.
				if len(repo.completions) != 1 || string(repo.completions[0].Output) != `{"target":9007199254740993}` ||
					values.env.Steps["forecast"].(map[string]any)["output"].(map[string]any)["target"] != int64(
						9007199254740993,
					) ||
					values.env.Vars["target"] != int64(9007199254740993) {
					t.Fatal("committed output did not feed owned, exact values")
				}
			} else if len(values.env.Steps) != 0 || len(values.env.Vars) != 1 {
				t.Fatal("failed Function advanced context")
			}
			if want := map[string]string{"unsupported output": "expression_value_invalid", "output container limit": "expression_limit", "capacity": "run_data_limit"}[fault]; want != "" {
				if len(repo.completions) != 1 || repo.completions[0].ValueFailure == nil ||
					repo.completions[0].ValueFailure.FailureCode != want {
					t.Fatal("provider success did not retain value failure")
				}
				if _, success := repo.completions[0].State.(FunctionSucceeded); !success {
					t.Fatal("provider success was falsified")
				}
			}
			if commandCalls == 1 && (string(device.inputs[0].Parameters) != `{"value":9007199254740993}` ||
				string(repo.inputs[1].Evidence.(CommandStarted).Parameters) != string(device.inputs[0].Parameters) ||
				device.inputs[0].EntityID != "ent_01980000-0000-7000-8000-000000000001" || device.inputs[0].OperationName != "set") {
				t.Fatal("Command dispatch differs from committed inputs")
			}
			if sends == 1 && (notifier.inputs[0].Message.Title != "Target 9007199254740993" ||
				repo.inputs[2].Evidence.(NotificationStarted).Message.Title != "Target 9007199254740993" ||
				repo.inputs[2].Evidence.(NotificationStarted).Message.Text != "Resolved" ||
				notifier.inputs[0].DestinationID != "ndst_01980000-0000-7000-8000-000000000001") {
				t.Fatal("Notification dispatch or reservation changed")
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

func TestEffectInputResolutionFailsBeforeReservation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		body StepBody
		path string
		code string
	}{
		{CommandStep{
			EntityID: "ent_01980000-0000-7000-8000-000000000001", OperationName: "set",
			Parameters: flowExpression(`vars.private_value.nested`),
		}, "/steps/0/parameters_expr", "expression_evaluation"},
		{FunctionStep{
			AdapterID: "weather", FunctionKey: "forecast",
			ContractFingerprint: "sha256-v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Arguments:           flowExpression(`vars.private_value.nested`),
		}, "/steps/0/arguments_expr", "expression_evaluation"},
		{NotificationStep{
			DestinationID: "ndst_01980000-0000-7000-8000-000000000001",
			Title:         flowExpression(`42`), Message: flowExpression(`vars.private_value.nested`),
		}, "/steps/0/title_expr", failureResolvedInputInvalid},
		{NotificationStep{
			DestinationID: "ndst_01980000-0000-7000-8000-000000000001",
			Title:         flowLiteral(`"Title"`), Message: flowExpression(`nil`),
		}, "/steps/0/message_expr", failureResolvedInputInvalid},
	} {
		t.Run(test.path, func(t *testing.T) {
			t.Parallel()
			definition := flowDefinition(t, map[VariableName]ValueBinding{"seed": flowLiteral(`9007199254740993`)},
				[]Step{{ID: "effect", Body: test.body}})
			run := flowRun(definition)
			repo := &flowEffectRepository{}
			repo.snapshot = &definition
			device, functions, notifier := &flowCommandDevices{}, &flowFunctionService{}, &flowNotificationService{}
			service := NewService(repo, device, Dependencies{Functions: functions, Notifications: notifier})
			values, err := newRunValues(run)
			if err != nil || !service.initializeValues(t.Context(), run, values) {
				t.Fatalf("initialize: %v", err)
			}
			if service.executeSequence(t.Context(), run, definition.Steps, values) || len(repo.inputs) != 0 ||
				len(device.inputs) != 0 || len(functions.inputs) != 0 || len(notifier.inputs) != 0 {
				t.Fatal("resolution failure reserved or dispatched external work")
			}
			if repo.failure == nil || repo.failure.Path != test.path || repo.failure.FailureCode != test.code ||
				len(repo.records) != 1 || repo.interrupts != 0 {
				t.Fatalf("resolution failure lost its field or classification: %#v", repo.failure)
			}
			if len(values.env.Steps) != 0 || len(values.env.Vars) != 1 ||
				values.env.Vars["seed"] != int64(9007199254740993) {
				t.Fatal("resolution failure advanced context")
			}
		})
	}
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
					Conditions: Condition{ID: "true", Body: ExpressionCondition{Value: flowExpression(`true`)}},
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
