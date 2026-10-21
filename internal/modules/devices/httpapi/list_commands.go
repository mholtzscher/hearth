package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
)

func registerListCommands(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-commands", Method: http.MethodGet, Path: "/commands",
		Summary: "List household Command history", Tags: []string{commandsTag},
		Errors: []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, handler.listCommands)
}

type ListCommandsInput struct {
	Limit    int    `query:"limit"     default:"50" minimum:"1" maximum:"200"`
	Cursor   string `query:"cursor"`
	EntityID string `query:"entity_id"`
	Status   string `query:"status"`
}

type ListCommandsOutput struct {
	Body CommandCollectionBody
}

func (handler *handler) listCommands(ctx context.Context, input *ListCommandsInput) (*ListCommandsOutput, error) {
	params := devices.ListCommandsParams{Limit: input.Limit}
	if input.EntityID != "" {
		entityID, err := devices.ParseEntityID(input.EntityID)
		if err != nil {
			return nil, apiError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
		}
		params.EntityID = &entityID
	}
	if input.Status != "" {
		status := devices.CommandStatus(input.Status)
		if !devices.ValidCommandStatus(status) {
			return nil, apiError(http.StatusBadRequest, "invalid status")
		}
		params.Status = &status
	}
	if input.Cursor != "" {
		requestedAt, commandID, cursorErr := apicursor.DecodeCommandListCursor(
			input.Cursor,
			params.EntityID,
			params.Status,
		)
		if cursorErr != nil {
			return nil, apiError(http.StatusBadRequest, "invalid cursor")
		}
		params.BeforeRequestedAt = requestedAt
		params.BeforeID = commandID
	}
	page, err := handler.devices.ListCommands(ctx, params)
	if errors.Is(err, devices.ErrInvalidPage) {
		return nil, apiError(http.StatusBadRequest, "invalid page")
	}
	if err != nil {
		return nil, internalAPIError(err)
	}
	body := CommandCollectionBody{Items: make([]CommandRecordBody, len(page.Items))}
	for index, command := range page.Items {
		mapped, mappingErr := commandRecordBody(command)
		if mappingErr != nil {
			return nil, internalAPIError(mappingErr)
		}
		body.Items[index] = mapped
	}
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		cursor, cursorErr := apicursor.EncodeCommandListCursor(last, params.EntityID, params.Status)
		if cursorErr != nil {
			return nil, internalAPIError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return &ListCommandsOutput{Body: body}, nil
}
