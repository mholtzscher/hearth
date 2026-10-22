//nolint:goconst // Literal wire keys keep the fixed success contracts readable.
package mcpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerExecuteEntityCommand(server *mcptools.Server, h *handler) {
	mcptools.RegisterWithRequest(
		server,
		mcptools.ToolWithRequest[ExecuteEntityCommandInput, CommandReceipt]{
			Name:        "execute_entity_command",
			Description: "Execute once and wait for terminal satisfaction or dispatch; inspect the receipt identity with get_command.",
			Output:      mustContract(mcptools.PrepareOutput[CommandReceipt](commandReceiptSchema())),
			Handler:     h.executeEntityCommand,
		},
	)
}

type ExecuteEntityCommandInput struct {
	EntityID   entityID       `json:"entity_id"  jsonschema:"canonical Hearth Entity ID"`
	Operation  string         `json:"operation"  jsonschema:"Entity type operation name, such as set"`
	Parameters map[string]any `json:"parameters" jsonschema:"operation parameters as a JSON object"`
}

func (input *ExecuteEntityCommandInput) UnmarshalJSON(data []byte) error {
	type arguments ExecuteEntityCommandInput
	var decoded arguments
	if err := decodeArguments(data, &decoded); err != nil {
		return err
	}
	*input = ExecuteEntityCommandInput(decoded)
	return nil
}

type CommandReceipt struct {
	CommandID     string          `json:"command_id"`
	Status        string          `json:"status"`
	ObservationID *string         `json:"observation_id,omitempty"`
	Value         json.RawMessage `json:"value,omitempty"`
}

// ExecuteCommand owns the blocking wait and durable lifecycle. The adapter calls
// it once and maps only the returned outcome, without reading or retrying it.
func (h *handler) executeEntityCommand(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input ExecuteEntityCommandInput,
) (CommandReceipt, error) {
	// SDK validation/defaults precede this handler, but its decoded map has
	// float64 numbers. The request retains the original opaque JSON document.
	var raw struct {
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(request.Params.Arguments, &raw); err != nil {
		return CommandReceipt{}, invalidRequestFailure("parameters must be a JSON object")
	}
	result, err := h.devices.ExecuteCommand(ctx, devices.CommandInput{EntityID: devices.EntityID(input.EntityID),
		OperationName: devices.OperationName(input.Operation), Parameters: devices.CommandParameters(raw.Parameters)})
	if err != nil {
		return CommandReceipt{}, commandFailure(err)
	}
	if result.Outcome == devices.OutcomeDispatched {
		return CommandReceipt{CommandID: string(result.CommandID), Status: "dispatched"}, nil
	}
	if result.ObservationID == nil || result.Value == nil {
		return CommandReceipt{}, mcptools.InternalToolError(errors.New("observed Command result lacks evidence"))
	}
	id := string(*result.ObservationID)
	return CommandReceipt{
		CommandID:     string(result.CommandID),
		Status:        "satisfied",
		ObservationID: &id,
		Value:         json.RawMessage(*result.Value),
	}, nil
}

func commandReceiptSchema() map[string]any {
	return map[string]any{"oneOf": []any{
		recordSchema(map[string]any{
			"command_id": stringSchema(), "status": map[string]any{"const": "dispatched"},
		}, "command_id", "status"),
		recordSchema(map[string]any{
			"command_id": stringSchema(), "status": map[string]any{"const": "satisfied"},
			"observation_id": stringSchema(), "value": map[string]any{},
		}, "command_id", "status", "observation_id", "value"),
	}}
}

const (
	// failureEntityDisabled is the durable code for a disabled Entity.
	failureEntityDisabled = failureCode(devices.CommandFailureEntityDisabled)
	// failureAdapterUnhealthy is the durable code for an unhealthy Adapter.
	failureAdapterUnhealthy = failureCode(devices.CommandFailureAdapterUnhealthy)
	// failureEntityUnavailable is the durable code for an unavailable Entity.
	failureEntityUnavailable = failureCode(devices.CommandFailureEntityUnavailable)
	// failureUpstreamRejected is the durable code for an Adapter rejection.
	failureUpstreamRejected = failureCode(devices.CommandFailureUpstreamRejected)
	// failureOutcomeTimeout is the durable code for an expired outcome deadline.
	failureOutcomeTimeout = failureCode(devices.CommandFailureOutcomeTimeout)
	// failureCommandUnavailable reports closed Command admission.
	failureCommandUnavailable failureCode = "command_unavailable"
)

// Structured error field names mcpapi attaches to a ToolError's structured
// isError result, so these keys are the machine-readable half of the failure
// contract.
const (
	detailStatus    = "status"
	detailCommandID = "command_id"
)

// commandFailure maps one failed execute_entity_command to the tool error the
// agent branches on, reading the same domain classification the HTTP route maps.
// The failure code and the durable Command status cross the boundary; the HTTP
// status does not.
func commandFailure(err error) *mcptools.ToolError {
	failure, classified := classifyCommandFailure(err)
	if !classified {
		return mcptools.InternalToolError(err)
	}
	if failure.durableStatus == "" {
		return toolError(failure.code, failure.message)
	}
	return commandToolError(failure.code, failure.message, failedCommandID(err), failure.durableStatus)
}

// commandToolError builds one Command failure result carrying the durable
// Command status, so the agent can correlate the failure code with the record
// get_command returns.
func commandToolError(
	code failureCode,
	message string,
	commandID string,
	status devices.CommandStatus,
) *mcptools.ToolError {
	details := map[string]any{detailStatus: string(status)}
	if commandID != "" {
		details[detailCommandID] = commandID
	}
	return &mcptools.ToolError{Code: string(code), Message: message, Details: details}
}

func failedCommandID(err error) string {
	var executionError *devices.CommandExecutionError
	if !errors.As(err, &executionError) {
		return ""
	}
	return string(executionError.CommandID)
}

type commandFailureClass struct {
	code          failureCode
	message       string
	durableStatus devices.CommandStatus
}

func classifyCommandFailure(err error) (commandFailureClass, bool) {
	switch {
	case errors.Is(err, devices.ErrEntityDisabled):
		return commandFailureClass{
			code: failureEntityDisabled, message: "entity is disabled",
			durableStatus: devices.CommandStatusEntityDisabled,
		}, true
	case errors.Is(err, devices.ErrCommandUnavailable):
		return commandFailureClass{
			code: failureCommandUnavailable, message: "command admission is unavailable",
		}, true
	case errors.Is(err, devices.ErrInvalidCommand):
		return commandFailureClass{
			code: failureInvalidRequest, message: "invalid command",
		}, true
	case errors.Is(err, devices.ErrEntityNotFound):
		return commandFailureClass{
			code: failureEntityNotFound, message: "entity not found",
		}, true
	case errors.Is(err, adapters.ErrAdapterUnhealthy):
		return commandFailureClass{
			code: failureAdapterUnhealthy, message: "adapter unhealthy",
			durableStatus: devices.CommandStatusAdapterUnhealthy,
		}, true
	case errors.Is(err, devices.ErrEntityUnavailable):
		return commandFailureClass{
			code: failureEntityUnavailable, message: "entity unavailable",
			durableStatus: devices.CommandStatusEntityUnavailable,
		}, true
	case errors.Is(err, devices.ErrUpstreamRejected):
		return commandFailureClass{
			code: failureUpstreamRejected, message: "upstream rejected command",
			durableStatus: devices.CommandStatusRejected,
		}, true
	case errors.Is(err, devices.ErrOutcomeTimeout):
		return commandFailureClass{
			code: failureOutcomeTimeout, message: "command outcome timed out",
			durableStatus: devices.CommandStatusOutcomeTimeout,
		}, true
	default:
		return commandFailureClass{}, false
	}
}
