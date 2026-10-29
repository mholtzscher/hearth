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
		return nil, huma.NewError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	parameters, marshalErr := json.Marshal(input.Body.Parameters)
	if marshalErr != nil {
		return nil, huma.NewError(http.StatusBadRequest, "parameters must be a JSON object")
	}
	result, commandErr := handler.devices.ExecuteCommand(ctx, devices.CommandInput{
		EntityID:      entityID,
		OperationName: devices.OperationName(input.Body.OperationName),
		Parameters:    devices.CommandParameters(parameters),
	})
	if commandErr != nil {
		problem := mapCommandError(commandErr)
		status, ok := errors.AsType[huma.StatusError](problem)
		// Unexpected execution errors already have a worker diagnostic. HTTP
		// owns only admission errors that never reached that failure boundary.
		_, executionOwned := errors.AsType[*devices.CommandExecutionError](commandErr)
		if ok && status.GetStatus() == http.StatusInternalServerError && !executionOwned {
			handler.logInternalFailure(ctx, "execute-entity-command", "service", commandErr)
		}
		return nil, problem
	}
	body, bodyErr := commandResultBody(result)
	if bodyErr != nil {
		return nil, handler.internalAPIError(ctx, "execute-entity-command", "mapping", bodyErr)
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

func mapCommandError(err error) error {
	switch {
	case errors.Is(err, devices.ErrEntityDisabled):
		executionError, hasRecord := errors.AsType[*devices.CommandExecutionError](err)
		if !hasRecord {
			return huma.NewError(http.StatusInternalServerError, "internal error")
		}
		return entityDisabledProblem(executionError.CommandID)
	case errors.Is(err, devices.ErrCommandUnavailable):
		return huma.NewError(http.StatusServiceUnavailable, "command admission is unavailable")
	case errors.Is(err, devices.ErrInvalidCommand):
		return huma.NewError(http.StatusBadRequest, "invalid command")
	case errors.Is(err, devices.ErrEntityNotFound):
		return huma.NewError(http.StatusNotFound, "entity not found")
	case errors.Is(err, adapters.ErrAdapterUnhealthy):
		return huma.NewError(http.StatusServiceUnavailable, "adapter unhealthy")
	case errors.Is(err, devices.ErrEntityUnavailable):
		return huma.NewError(http.StatusServiceUnavailable, "entity unavailable")
	case errors.Is(err, devices.ErrUpstreamRejected):
		return huma.NewError(http.StatusBadGateway, "upstream rejected command")
	case errors.Is(err, devices.ErrOutcomeTimeout):
		return huma.NewError(http.StatusGatewayTimeout, "command outcome timed out")
	default:
		return huma.NewError(http.StatusInternalServerError, "internal error")
	}
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
