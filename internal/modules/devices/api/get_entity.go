package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type GetEntityInput struct {
	EntityID string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
}

type GetEntityOutput struct {
	Body EntityBody
}

func (handler *Handler) GetEntity(ctx context.Context, input *GetEntityInput) (*GetEntityOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	view, err := handler.devices.GetEntity(ctx, entityID)
	if errors.Is(err, devices.ErrEntityNotFound) {
		return nil, apiError(http.StatusNotFound, "entity not found")
	}
	if err != nil {
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	body, err := entityBody(view)
	if err != nil {
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	return &GetEntityOutput{Body: body}, nil
}
