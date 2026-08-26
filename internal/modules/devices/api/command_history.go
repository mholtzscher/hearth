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
	Limit    int    `query:"limit" default:"50" minimum:"1" maximum:"200"`
	Cursor   string `query:"cursor"`
}

type ListEntityCommandsOutput struct {
	Body CommandCollectionBody
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
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	body, err := commandRecordBody(command)
	if err != nil {
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	return &GetCommandOutput{Body: body}, nil
}

func (handler *Handler) ListEntityCommands(ctx context.Context, input *ListEntityCommandsInput) (*ListEntityCommandsOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	params := devices.ListEntityCommandsParams{EntityID: entityID, Limit: input.Limit}
	if input.Cursor != "" {
		requestedAt, commandID, err := decodeCommandCursor(input.Cursor, entityID)
		if err != nil {
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
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	body := CommandCollectionBody{Items: make([]CommandRecordBody, len(page.Items))}
	for index, command := range page.Items {
		mapped, err := commandRecordBody(command)
		if err != nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		body.Items[index] = mapped
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, err := encodeCommandCursor(page.Items[len(page.Items)-1])
		if err != nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		body.NextCursor = &cursor
	}
	return &ListEntityCommandsOutput{Body: body}, nil
}
