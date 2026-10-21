package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
)

func registerListEntityCommands(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-entity-commands", Method: http.MethodGet, Path: "/entities/{entity_id}/commands",
		Summary: "List an Entity's Command history", Tags: []string{commandsTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.listEntityCommands)
}

type ListEntityCommandsInput struct {
	EntityID string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Limit    int    `                                                  query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor   string `                                                  query:"cursor"`
}

type ListEntityCommandsOutput struct {
	Body CommandCollectionBody
}

func (handler *handler) listEntityCommands(
	ctx context.Context,
	input *ListEntityCommandsInput,
) (*ListEntityCommandsOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	params := devices.ListEntityCommandsParams{EntityID: entityID, Limit: input.Limit}
	if input.Cursor != "" {
		requestedAt, commandID, cursorErr := apicursor.DecodeCommandCursor(input.Cursor, entityID)
		if cursorErr != nil {
			return nil, apiError(http.StatusBadRequest, "invalid cursor")
		}
		params.BeforeRequestedAt = requestedAt
		params.BeforeID = commandID
	}
	page, err := handler.devices.ListEntityCommands(ctx, params)
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return nil, apiError(http.StatusBadRequest, "invalid page")
	case errors.Is(err, devices.ErrEntityNotFound):
		return nil, apiError(http.StatusNotFound, "entity not found")
	case err != nil:
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
		cursor, cursorErr := apicursor.EncodeCommandCursor(page.Items[len(page.Items)-1])
		if cursorErr != nil {
			return nil, internalAPIError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return &ListEntityCommandsOutput{Body: body}, nil
}
