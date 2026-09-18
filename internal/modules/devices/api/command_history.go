package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type GetCommandInput struct {
	CommandID string `path:"command_id" doc:"Canonical Hearth Command ID"`
}

type GetCommandOutput struct {
	Body CommandRecordBody
}

type ListEntityCommandsInput struct {
	EntityID string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Limit    int    `                                                  query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor   string `                                                  query:"cursor"`
}

type ListEntityCommandsOutput struct {
	Body CommandCollectionBody
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

func (handler *Handler) ListCommands(ctx context.Context, input *ListCommandsInput) (*ListCommandsOutput, error) {
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
		requestedAt, commandID, cursorErr := decodeCommandListCursor(input.Cursor, params.EntityID, params.Status)
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
		cursor, cursorErr := encodeCommandListCursor(last, params.EntityID, params.Status)
		if cursorErr != nil {
			return nil, internalAPIError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return &ListCommandsOutput{Body: body}, nil
}

func (handler *Handler) GetCommand(ctx context.Context, input *GetCommandInput) (*GetCommandOutput, error) {
	commandID, err := devices.ParseCommandID(input.CommandID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "command_id must be a canonical Hearth Command ID")
	}
	command, err := handler.devices.GetCommand(ctx, commandID)
	if errors.Is(err, devices.ErrCommandNotFound) {
		return nil, apiError(http.StatusNotFound, "command not found")
	}
	if err != nil {
		return nil, internalAPIError(err)
	}
	body, err := commandRecordBody(command)
	if err != nil {
		return nil, internalAPIError(err)
	}
	return &GetCommandOutput{Body: body}, nil
}

func (handler *Handler) ListEntityCommands(
	ctx context.Context,
	input *ListEntityCommandsInput,
) (*ListEntityCommandsOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	params := devices.ListEntityCommandsParams{EntityID: entityID, Limit: input.Limit}
	if input.Cursor != "" {
		requestedAt, commandID, cursorErr := decodeCommandCursor(input.Cursor, entityID)
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
		cursor, cursorErr := encodeCommandCursor(page.Items[len(page.Items)-1])
		if cursorErr != nil {
			return nil, internalAPIError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return &ListEntityCommandsOutput{Body: body}, nil
}
