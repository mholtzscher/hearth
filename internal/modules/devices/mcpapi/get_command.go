//nolint:goconst // Literal wire keys keep the success constraints local.
package mcpapi

import (
	"context"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerGetCommand(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[GetCommandInput, CommandDetail]{
			Name:        "get_command",
			Description: "Inspect a retained Command including exact parameters and outcome identity.",
			Handler:     h.getCommand,
		},
	)
}

type GetCommandInput struct {
	CommandID commandID `json:"command_id" doc:"canonical Hearth Command ID"`
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

func (CommandDetail) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Properties["parameters"] = &huma.Schema{Type: "object"}
	// Only inherited summary fields need their nullable contract narrowed here.
	for _, name := range []string{"completed_at", "failure_code"} {
		schema.Properties[name] = stringSchema()
	}
	return schema
}
