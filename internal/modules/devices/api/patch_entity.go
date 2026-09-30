package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type PatchEntityInput struct {
	EntityID string          `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Body     PatchEntityBody `                 doc:"Mutable Entity fields"`
}

type PatchEntityOutput struct {
	Body EntityBody
}

func (handler *Handler) PatchEntity(ctx context.Context, input *PatchEntityInput) (*PatchEntityOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	view, err := handler.devices.PatchEntity(ctx, entityID, devices.EntityPatch{
		Enabled: input.Body.Enabled, NameEdit: domainNameEdit(input.Body.NameEdit),
	})
	switch {
	case errors.Is(err, devices.ErrInvalidMetadataPatch):
		return nil, apiError(http.StatusBadRequest, "invalid metadata patch")
	case errors.Is(err, devices.ErrEntityNotFound):
		return nil, apiError(http.StatusNotFound, "entity not found")
	case err != nil:
		return nil, internalAPIError(err)
	}
	body, err := entityBody(view)
	if err != nil {
		return nil, internalAPIError(err)
	}
	return &PatchEntityOutput{Body: body}, nil
}
