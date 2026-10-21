package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func registerExecuteEntityCommand(api huma.API, handler *handler) {
	disabledProblemSchema := huma.SchemaFromType(
		api.OpenAPI().Components.Schemas,
		reflect.TypeFor[disabledCommandError](),
	)
	huma.Register(api, huma.Operation{
		OperationID: "execute-entity-command", Method: http.MethodPost, Path: "/entities/{entity_id}/commands",
		Summary: "Execute an Entity Command", Tags: []string{entitiesTag},
		Errors: []int{
			http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusInternalServerError,
		},
		Responses: map[string]*huma.Response{
			"409": {
				Description: http.StatusText(http.StatusConflict),
				Content: map[string]*huma.MediaType{
					"application/problem+json": {Schema: disabledProblemSchema},
				},
			},
		},
	}, handler.executeCommand)
}

type ExecuteCommandInput struct {
	EntityID string      `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Body     CommandBody `                 doc:"Entity operation and parameters"`
}

type ExecuteCommandOutput struct {
	Body CommandResultBody
}

func (handler *handler) executeCommand(ctx context.Context, input *ExecuteCommandInput) (*ExecuteCommandOutput, error) {
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
	body, bodyErr := commandResultBody(result)
	if bodyErr != nil {
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	return &ExecuteCommandOutput{Body: body}, nil
}

const (
	commandStatusSatisfied  = "satisfied"
	commandStatusDispatched = "dispatched"
)

var errCommandResultMapping = errors.New("command result cannot be mapped to a response body")

func commandResultBody(result devices.CommandResult) (CommandResultBody, error) {
	if result.Outcome == devices.OutcomeDispatched {
		return CommandResultBody{
			CommandID: string(result.CommandID), Status: commandStatusDispatched,
		}, nil
	}
	if result.ObservationID == nil || result.Value == nil {
		return CommandResultBody{}, errCommandResultMapping
	}
	var value any
	if err := decodeJSON(*result.Value, &value); err != nil {
		return CommandResultBody{}, errCommandResultMapping
	}
	observationID := string(*result.ObservationID)
	return CommandResultBody{
		CommandID: string(result.CommandID), Status: commandStatusSatisfied,
		ObservationID: &observationID, Value: &value,
	}, nil
}

type commandFailure struct {
	code          string
	message       string
	httpStatus    int
	durableStatus devices.CommandStatus
}

func classifyCommandFailure(err error) (commandFailure, bool) {
	switch {
	case errors.Is(err, devices.ErrEntityDisabled):
		return commandFailure{
			code: "entity_disabled", message: "entity is disabled",
			httpStatus: http.StatusConflict, durableStatus: devices.CommandStatusEntityDisabled,
		}, true
	case errors.Is(err, devices.ErrCommandUnavailable):
		return commandFailure{
			code: "command_unavailable", message: "command admission is unavailable",
			httpStatus: http.StatusServiceUnavailable,
		}, true
	case errors.Is(err, devices.ErrInvalidCommand):
		return commandFailure{
			code: "invalid_request", message: "invalid command",
			httpStatus: http.StatusBadRequest,
		}, true
	case errors.Is(err, devices.ErrEntityNotFound):
		return commandFailure{
			code: "entity_not_found", message: "entity not found",
			httpStatus: http.StatusNotFound,
		}, true
	case errors.Is(err, adapters.ErrAdapterUnhealthy):
		return commandFailure{
			code: "adapter_unhealthy", message: "adapter unhealthy",
			httpStatus: http.StatusServiceUnavailable, durableStatus: devices.CommandStatusAdapterUnhealthy,
		}, true
	case errors.Is(err, devices.ErrEntityUnavailable):
		return commandFailure{
			code: "entity_unavailable", message: "entity unavailable",
			httpStatus: http.StatusServiceUnavailable, durableStatus: devices.CommandStatusEntityUnavailable,
		}, true
	case errors.Is(err, devices.ErrUpstreamRejected):
		return commandFailure{
			code: "upstream_rejected", message: "upstream rejected command",
			httpStatus: http.StatusBadGateway, durableStatus: devices.CommandStatusRejected,
		}, true
	case errors.Is(err, devices.ErrOutcomeTimeout):
		return commandFailure{
			code: "outcome_timeout", message: "command outcome timed out",
			httpStatus: http.StatusGatewayTimeout, durableStatus: devices.CommandStatusOutcomeTimeout,
		}, true
	default:
		return commandFailure{}, false
	}
}

func mapCommandError(err error) error {
	failure, classified := classifyCommandFailure(err)
	if !classified {
		return apiError(http.StatusInternalServerError, "internal error")
	}
	if failure.durableStatus == devices.CommandStatusEntityDisabled {
		executionError, hasRecord := errors.AsType[*devices.CommandExecutionError](err)
		if !hasRecord {
			return apiError(http.StatusInternalServerError, "internal error")
		}
		return entityDisabledProblem(executionError.CommandID)
	}
	return apiError(failure.httpStatus, failure.message)
}

type CommandBody struct {
	OperationName string         `json:"operation"`
	Parameters    map[string]any `json:"parameters"`
}

type CommandResultBody struct {
	CommandID     string  `json:"command_id"`
	Status        string  `json:"status"`                   // "satisfied" | "dispatched"
	ObservationID *string `json:"observation_id,omitempty"` // present iff satisfied
	Value         *any    `json:"value,omitempty"`          // present iff satisfied
}

type disabledCommandError struct {
	Type      string `json:"type"       format:"uri" default:"about:blank"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	CommandID string `json:"command_id"`
}

func (problem *disabledCommandError) Error() string  { return problem.Detail }
func (problem *disabledCommandError) GetStatus() int { return problem.Status }
func (problem *disabledCommandError) ContentType(contentType string) string {
	if contentType == "application/json" {
		return "application/problem+json"
	}
	return contentType
}

func entityDisabledProblem(commandID devices.CommandID) error {
	return &disabledCommandError{
		Type: "about:blank", Title: http.StatusText(http.StatusConflict), Status: http.StatusConflict,
		Detail: "entity is disabled", Code: "entity_disabled", CommandID: string(commandID),
	}
}
