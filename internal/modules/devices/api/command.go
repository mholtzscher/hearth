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
	Body     CommandBody `                 doc:"Entity operation and parameters"`
}

type ExecuteCommandOutput struct {
	Body CommandResultBody
}

func (handler *Handler) ExecuteCommand(ctx context.Context, input *ExecuteCommandInput) (*ExecuteCommandOutput, error) {
	entityID, parseErr := devices.ParseEntityID(input.EntityID)
	if parseErr != nil {
		return nil, apiError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	parameters, marshalErr := json.Marshal(input.Body.Parameters)
	if marshalErr != nil {
		return nil, apiError(http.StatusBadRequest, "parameters must be a JSON object")
	}
	result, commandErr := handler.devices.ExecuteCommand(ctx, devices.CommandInput{
		EntityID:      entityID,
		OperationName: devices.OperationName(input.Body.OperationName),
		Parameters:    devices.CommandParameters(parameters),
	})
	if commandErr != nil {
		return nil, mapCommandError(commandErr)
	}
	status := "satisfied"
	var observationID *string
	var bodyValue *any
	if result.Outcome == devices.OutcomeDispatched {
		status = "dispatched"
	} else {
		if result.ObservationID == nil || result.Value == nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		var value any
		if err := decodeJSON(*result.Value, &value); err != nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		id := string(*result.ObservationID)
		observationID = &id
		bodyValue = &value
	}
	return &ExecuteCommandOutput{Body: CommandResultBody{
		CommandID: string(result.CommandID), Status: status,
		ObservationID: observationID, Value: bodyValue,
	}}, nil
}

func mapCommandError(err error) error {
	var executionError *devices.CommandExecutionError
	if errors.Is(err, devices.ErrEntityDisabled) && errors.As(err, &executionError) {
		return entityDisabledProblem(executionError.CommandID)
	}
	switch {
	case errors.Is(err, devices.ErrInvalidCommand):
		return apiError(http.StatusBadRequest, "invalid command")
	case errors.Is(err, devices.ErrEntityNotFound):
		return apiError(http.StatusNotFound, "entity not found")
	case errors.Is(err, devices.ErrAdapterUnhealthy):
		return apiError(http.StatusServiceUnavailable, "adapter unhealthy")
	case errors.Is(err, devices.ErrEntityUnavailable):
		return apiError(http.StatusServiceUnavailable, "entity unavailable")
	case errors.Is(err, devices.ErrUpstreamRejected):
		return apiError(http.StatusBadGateway, "upstream rejected command")
	case errors.Is(err, devices.ErrOutcomeTimeout):
		return apiError(http.StatusGatewayTimeout, "command outcome timed out")
	default:
		return apiError(http.StatusInternalServerError, "internal error")
	}
}
