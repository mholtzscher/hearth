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
		return nil, apiError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	parameters, err := json.Marshal(input.Body.Parameters)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "parameters must be a JSON object")
	}
	result, err := handler.devices.ExecuteCommand(
		ctx, entityID, devices.OperationName(input.Body.OperationName), devices.CommandParameters(parameters),
	)
	if err != nil {
		return nil, mapCommandError(err)
	}
	var value any
	if err := decodeJSON(result.Value, &value); err != nil {
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	return &ExecuteCommandOutput{Body: CommandResultBody{
		CommandID: string(result.CommandID), Status: "satisfied",
		ObservationID: string(result.ObservationID), Value: value,
	}}, nil
}

func mapCommandError(err error) error {
	switch {
	case errors.Is(err, devices.ErrInvalidCommand):
		return apiError(http.StatusBadRequest, "invalid command")
	case errors.Is(err, devices.ErrEntityNotFound):
		return apiError(http.StatusNotFound, "entity not found")
	case errors.Is(err, devices.ErrAdapterUnavailable):
		return apiError(http.StatusServiceUnavailable, "adapter unavailable")
	case errors.Is(err, devices.ErrUpstreamRejected):
		return apiError(http.StatusBadGateway, "upstream rejected command")
	case errors.Is(err, devices.ErrOutcomeTimeout):
		return apiError(http.StatusGatewayTimeout, "command outcome timed out")
	default:
		return apiError(http.StatusInternalServerError, "internal error")
	}
}
