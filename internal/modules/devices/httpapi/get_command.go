//nolint:dupl // Each operation owns its registration, request, and domain translation.
package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func registerGetCommand(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "get-command", Method: http.MethodGet, Path: "/commands/{command_id}",
		Summary: "Get a Command record", Tags: []string{commandsTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.getCommand)
}

type GetCommandInput struct {
	CommandID string `path:"command_id" doc:"Canonical Hearth Command ID"`
}

type GetCommandOutput struct {
	Body CommandRecordBody
}

func (handler *handler) getCommand(ctx context.Context, input *GetCommandInput) (*GetCommandOutput, error) {
	commandID, err := devices.ParseCommandID(input.CommandID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "command_id must be a canonical Hearth Command ID")
	}
	command, err := handler.devices.GetCommand(ctx, commandID)
	if errors.Is(err, devices.ErrCommandNotFound) {
		return nil, huma.NewError(http.StatusNotFound, "command not found")
	}
	if err != nil {
		return nil, handler.internalAPIError(ctx, "get-command", "service", err)
	}
	body, err := commandRecordBody(command)
	if err != nil {
		return nil, handler.internalAPIError(ctx, "get-command", "mapping", err)
	}
	return &GetCommandOutput{Body: body}, nil
}
