//nolint:goconst // Literal wire keys keep the fixed success contracts readable.
package mcpapi

import (
	"context"
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerGetCommand(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[GetCommandInput, CommandDetail]{
			Name:        "get_command",
			Description: "Inspect a retained Command including exact parameters and outcome identity.",
			Output:      mustContract(mcptools.PrepareOutput[CommandDetail](commandDetailSchema())),
			Handler:     h.getCommand,
		},
	)
}

type GetCommandInput struct {
	CommandID commandID `json:"command_id" jsonschema:"canonical Hearth Command ID"`
}

type CommandDetail struct {
	CommandSummary

	Parameters           json.RawMessage `json:"parameters"`
	DeadlineAt           string          `json:"deadline_at"`
	AcceptedAt           *string         `json:"accepted_at,omitempty"`
	OutcomeObservationID *string         `json:"outcome_observation_id,omitempty"`
}

func (h *handler) getCommand(ctx context.Context, input GetCommandInput) (CommandDetail, error) {
	command, err := h.devices.GetCommand(ctx, devices.CommandID(input.CommandID))
	if err != nil {
		return CommandDetail{}, readFailure(err)
	}
	result := CommandDetail{CommandSummary: commandSummary(command), Parameters: json.RawMessage(command.Parameters),
		DeadlineAt: formatTime(command.DeadlineAt), AcceptedAt: optionalTime(command.AcceptedAt)}
	if command.OutcomeObservationID != nil {
		value := string(*command.OutcomeObservationID)
		result.OutcomeObservationID = &value
	}
	return result, nil
}

func commandDetailSchema() map[string]any {
	return recordSchema(map[string]any{
		"command_id":             stringSchema(),
		"entity_id":              stringSchema(),
		"operation":              stringSchema(),
		"status":                 stringSchema(),
		"requested_at":           stringSchema(),
		"completed_at":           stringSchema(),
		"failure_code":           stringSchema(),
		"parameters":             map[string]any{"type": "object"},
		"deadline_at":            stringSchema(),
		"accepted_at":            stringSchema(),
		"outcome_observation_id": stringSchema(),
	}, "command_id", "entity_id", "operation", "status", "requested_at", "parameters", "deadline_at")
}

func (input *GetCommandInput) UnmarshalJSON(data []byte) error {
	type arguments GetCommandInput
	var decoded arguments
	if err := decodeArguments(data, &decoded); err != nil {
		return err
	}
	*input = GetCommandInput(decoded)
	return nil
}
