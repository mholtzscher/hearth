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

// The two terminal Command statuses a successful execute_entity_command reports
// in its response body.
const (
	commandStatusSatisfied  = "satisfied"
	commandStatusDispatched = "dispatched"
)

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
	body, bodyErr := commandResultBody(result)
	if bodyErr != nil {
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	return &ExecuteCommandOutput{Body: body}, nil
}

// errCommandResultMapping reports a terminal Command result the response body
// cannot represent: an observed outcome missing its evidence, or evidence that
// is not one JSON value. Both transports keep their own internal-error
// rendering for it.
var errCommandResultMapping = errors.New("command result cannot be mapped to a response body")

// commandResultBody maps one terminal Command result to the body the HTTP route
// and the MCP tool both return: the dispatched outcome omits the evidence, and
// the observed outcome carries it.
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

// commandFailure is the transport-neutral classification of one failed Command,
// so the HTTP route and the MCP tool describe one failure the same way: code is
// the stable machine-readable failure code MCP publishes (and HTTP publishes in
// the one problem document that carries one), message is the stable human
// summary both transports render, httpStatus is the status the HTTP problem
// document carries, and durableStatus is the Command status MCP publishes as
// error detail, empty when the failure has no durable Command record.
type commandFailure struct {
	code          mcpFailureCode
	message       string
	httpStatus    int
	durableStatus devices.CommandStatus
}

// classifyCommandFailure classifies one failed Command, reporting false when no
// Command sentinel names the failure, so each transport keeps its own
// internal-error rendering.
func classifyCommandFailure(err error) (commandFailure, bool) {
	switch {
	case errors.Is(err, devices.ErrEntityDisabled):
		return commandFailure{
			code: mcpFailureEntityDisabled, message: "entity is disabled",
			httpStatus: http.StatusConflict, durableStatus: devices.CommandStatusEntityDisabled,
		}, true
	case errors.Is(err, devices.ErrCommandUnavailable):
		return commandFailure{
			code: mcpFailureCommandUnavailable, message: "command admission is unavailable",
			httpStatus: http.StatusServiceUnavailable,
		}, true
	case errors.Is(err, devices.ErrInvalidCommand):
		return commandFailure{
			code: mcpFailureInvalidRequest, message: "invalid command",
			httpStatus: http.StatusBadRequest,
		}, true
	case errors.Is(err, devices.ErrEntityNotFound):
		return commandFailure{
			code: mcpFailureEntityNotFound, message: "entity not found",
			httpStatus: http.StatusNotFound,
		}, true
	case errors.Is(err, devices.ErrAdapterUnhealthy):
		return commandFailure{
			code: mcpFailureAdapterUnhealthy, message: "adapter unhealthy",
			httpStatus: http.StatusServiceUnavailable, durableStatus: devices.CommandStatusAdapterUnhealthy,
		}, true
	case errors.Is(err, devices.ErrEntityUnavailable):
		return commandFailure{
			code: mcpFailureEntityUnavailable, message: "entity unavailable",
			httpStatus: http.StatusServiceUnavailable, durableStatus: devices.CommandStatusEntityUnavailable,
		}, true
	case errors.Is(err, devices.ErrUpstreamRejected):
		return commandFailure{
			code: mcpFailureUpstreamRejected, message: "upstream rejected command",
			httpStatus: http.StatusBadGateway, durableStatus: devices.CommandStatusRejected,
		}, true
	case errors.Is(err, devices.ErrOutcomeTimeout):
		return commandFailure{
			code: mcpFailureOutcomeTimeout, message: "command outcome timed out",
			httpStatus: http.StatusGatewayTimeout, durableStatus: devices.CommandStatusOutcomeTimeout,
		}, true
	default:
		return commandFailure{}, false
	}
}

// mapCommandError renders one failed Command as the HTTP problem document. The
// classification supplies the status and summary; a disabled Entity additionally
// needs the durable Command ID its problem details extension carries, so an
// unrecorded disabled outcome stays an unpublished invariant.
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
