package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type ExecuteCommandInput struct {
	EntityID string      `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Body     CommandBody `doc:"Entity operation and parameters"`
}

type ExecuteCommandOutput struct {
	Body CommandResultBody
}

func (handler *Handler) ExecuteCommand(ctx context.Context, input *ExecuteCommandInput) (*ExecuteCommandOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "invalid_request", "entity_id must be a canonical Hearth Entity ID")
	}
	parameters, err := json.Marshal(input.Body.Parameters)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "invalid_request", "parameters must be a JSON object")
	}
	result, err := handler.commands.ExecuteCommand(
		ctx, entityID, devices.OperationName(input.Body.OperationName), devices.CommandParameters(parameters),
	)
	if err != nil {
		return nil, mapCommandError(err)
	}
	var value any
	if err := decodeJSON(result.Value, &value); err != nil {
		return nil, apiError(http.StatusInternalServerError, "internal_error", "internal error")
	}
	return &ExecuteCommandOutput{Body: CommandResultBody{
		CommandID: string(result.CommandID), Status: "satisfied",
		ObservationID: string(result.ObservationID), Value: value,
	}}, nil
}

func mapCommandError(err error) error {
	var executionError *devices.CommandExecutionError
	var commandID *string
	if errors.As(err, &executionError) {
		value := string(executionError.CommandID)
		commandID = &value
	}
	switch {
	case errors.Is(err, devices.ErrInvalidCommand):
		return apiCommandError(http.StatusBadRequest, "invalid_request", "invalid command", commandID)
	case errors.Is(err, devices.ErrEntityNotFound):
		return apiCommandError(http.StatusNotFound, "entity_not_found", "entity not found", commandID)
	case errors.Is(err, devices.ErrAdapterUnavailable):
		return apiCommandError(http.StatusServiceUnavailable, "adapter_unavailable", "adapter unavailable", commandID)
	case errors.Is(err, devices.ErrUpstreamRejected):
		return apiCommandError(http.StatusBadGateway, "upstream_rejected", "upstream rejected command", commandID)
	case errors.Is(err, devices.ErrOutcomeTimeout):
		return apiCommandError(http.StatusGatewayTimeout, "outcome_timeout", "command outcome timed out", commandID)
	default:
		return apiCommandError(http.StatusInternalServerError, "internal_error", "internal error", commandID)
	}
}

func apiCommandError(status int, code, message string, commandID *string) error {
	return &statusError{
		status: status,
		ErrorBody: ErrorBody{Error: APIError{
			Code: code, Message: message, CommandID: commandID,
		}},
	}
}
