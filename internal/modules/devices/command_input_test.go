package devices //nolint:testpackage // Tests exercise command validation without writable dependencies.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func commandValidationDependencies() Dependencies {
	return Dependencies{
		Now:              func() time.Time { panic("validation read the command clock") },
		NewCommandID:     func() (CommandID, error) { panic("validation generated a command ID") },
		NewCorrelationID: func() (CorrelationID, error) { panic("validation generated a correlation ID") },
	}
}

func TestValidateCommandNormalizesOwnedParametersWithoutSideEffects(t *testing.T) {
	t.Parallel()
	repository := newCommandRepository()
	repository.view.Entity.Enabled = false
	repository.view.Availability.Status = EntityAvailabilityUnavailable
	// Only the read capability exists: accidental writes, health checks or sends fail.
	service := NewService(Stores{Reads: repository}, nil, firstLightCatalog(t), commandValidationDependencies())
	input := CommandInput{EntityID: commandTestEntityID, OperationName: OperationNameSet,
		Parameters: CommandParameters(" { \"value\" : true } ")}
	normalized, err := service.ValidateCommand(context.Background(), input)
	if err != nil || string(normalized) != `{"value":true}` {
		t.Fatalf("normalized parameters = %s, error = %v", normalized, err)
	}
	normalized[0] = '['
	if string(input.Parameters) != " { \"value\" : true } " {
		t.Fatalf("validation aliased input parameters: %s", input.Parameters)
	}
	input.Parameters[0] = '\n'
	again, err := service.ValidateCommand(context.Background(), input)
	if err != nil || string(again) != `{"value":true}` {
		t.Fatalf("validation retained mutable result: %s, %v", again, err)
	}
}

func TestCommandValidationAndExecutionRejectInvalidInputsWithoutSideEffects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*CommandInput)
	}{
		{"command wrong prefix", func(in *CommandInput) {
			in.ID = CommandID(commandTestEntityID)
		}},
		{"command uppercase", func(in *CommandInput) {
			in.ID = "cmd_01890F47-7a6b-7c4d-8e9f-0123456789ab"
		}},
		{"command compact UUID", func(in *CommandInput) {
			in.ID = CommandID(strings.ReplaceAll(string(commandTestID), "-", ""))
		}},
		{"command version 4", func(in *CommandInput) {
			in.ID = "cmd_01890f47-7a6b-4c4d-8e9f-0123456789ab"
		}},
		{"command non RFC variant", func(in *CommandInput) {
			in.ID = "cmd_01890f47-7a6b-7c4d-ce9f-0123456789ab"
		}},
		{"correlation wrong prefix", func(in *CommandInput) {
			in.CorrelationID = CorrelationID(commandTestID)
		}},
		{"correlation uppercase", func(in *CommandInput) {
			in.CorrelationID = "cor_01890F47-7a6b-7c4d-8e9f-0123456789ab"
		}},
		{"correlation version 4", func(in *CommandInput) {
			in.CorrelationID = "cor_01890f47-7a6b-4c4d-8e9f-0123456789ab"
		}},
		{"correlation non RFC variant", func(in *CommandInput) {
			in.CorrelationID = "cor_01890f47-7a6b-7c4d-ce9f-0123456789ab"
		}},
		{"invalid target", func(in *CommandInput) {
			in.EntityID = "not-an-entity"
		}},
		{"unsafe operation", func(in *CommandInput) {
			in.OperationName = "set.*"
		}},
		{"unsupported operation", func(in *CommandInput) {
			in.OperationName = "toggle"
		}},
		{"wrong parameter type", func(in *CommandInput) {
			in.Parameters = CommandParameters(`{"value":1}`)
		}},
		{"unknown parameter", func(in *CommandInput) {
			in.Parameters = CommandParameters(`{"value":true,"extra":1}`)
		}},
		{"malformed JSON", func(in *CommandInput) {
			in.Parameters = CommandParameters(`{`)
		}},
		{"trailing JSON", func(in *CommandInput) {
			in.Parameters = CommandParameters(`{} {}`)
		}},
		{"array", func(in *CommandInput) {
			in.Parameters = CommandParameters(`[]`)
		}},
		{"null", func(in *CommandInput) {
			in.Parameters = CommandParameters(`null`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := NewService(
				Stores{Reads: newCommandRepository()}, nil, firstLightCatalog(t), commandValidationDependencies(),
			)
			input := CommandInput{EntityID: commandTestEntityID, OperationName: OperationNameSet,
				Parameters: CommandParameters(`{"value":true}`)}
			test.change(&input)
			parameters, err := service.ValidateCommand(context.Background(), input)
			if !errors.Is(err, ErrInvalidCommand) || parameters != nil {
				t.Fatalf("ValidateCommand = %s, %v", parameters, err)
			}
			if _, err = service.ExecuteCommand(context.Background(), input); !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("ExecuteCommand error = %v", err)
			}
		})
	}
}

func TestExecuteCommandGeneratesOnlyEmptyIdentities(t *testing.T) {
	t.Parallel()
	for _, supplied := range []string{"neither", "command", "correlation", "both"} {
		t.Run(supplied, func(t *testing.T) {
			t.Parallel()
			testCommandIdentityGeneration(t, supplied)
		})
	}
}

func testCommandIdentityGeneration(t *testing.T, supplied string) {
	t.Helper()
	input := CommandInput{
		EntityID: commandTestEntityID, OperationName: "trigger", Parameters: CommandParameters(`{"name":"blink"}`),
	}
	if supplied == "command" || supplied == "both" {
		input.ID = "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	}
	if supplied == "correlation" || supplied == "both" {
		input.CorrelationID = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ad"
	}
	commandGenerations, correlationGenerations := 0, 0
	dependencies := commandDependencies()
	dependencies.NewCommandID = func() (CommandID, error) { commandGenerations++; return commandTestID, nil }
	dependencies.NewCorrelationID = func() (CorrelationID, error) {
		correlationGenerations++
		return commandTestCorrelationID, nil
	}
	repository := newDispatchedCommandRepository()
	service := newTestService(repository, commandSenderFunc(func(
		context.Context, string, RuntimeID, CommandRequest,
	) (CommandAcceptance, error) {
		return CommandAcceptance{Accepted: true}, nil
	}), firstLightCatalog(t), dependencies)
	result, err := service.ExecuteCommand(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	wantID, wantCorrelation := input.ID, input.CorrelationID
	wantCommandGenerations, wantCorrelationGenerations := 0, 0
	if wantID == "" {
		wantID = commandTestID
		wantCommandGenerations = 1
	}
	if wantCorrelation == "" {
		wantCorrelation = commandTestCorrelationID
		wantCorrelationGenerations = 1
	}
	stored := repository.command(wantID)
	if result.CommandID != wantID || stored.CorrelationID != wantCorrelation ||
		commandGenerations != wantCommandGenerations || correlationGenerations != wantCorrelationGenerations {
		t.Fatalf("result = %#v, record = %#v, generations = %d/%d",
			result, stored, commandGenerations, correlationGenerations)
	}
}
