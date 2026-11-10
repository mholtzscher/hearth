package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func registerGetEntity(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "get-entity", Method: http.MethodGet, Path: "/entities/{entity_id}",
		Summary: "Get an Entity and its current State", Tags: []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.getEntity)
}

type GetEntityInput struct {
	EntityID string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Units    string `                 doc:"Omit for household units; canonical returns canonical values" query:"units"`
}

type GetEntityOutput struct {
	Body EntityBody
}

func (handler *handler) getEntity(ctx context.Context, input *GetEntityInput) (*GetEntityOutput, error) {
	units, err := devices.ParseReadUnits(input.Units)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "invalid units")
	}
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	view, err := handler.devices.GetEntity(ctx, entityID)
	if errors.Is(err, devices.ErrEntityNotFound) {
		return nil, huma.NewError(http.StatusNotFound, "entity not found")
	}
	if err != nil {
		return nil, handler.internalAPIError(ctx, "get-entity", "service", err)
	}
	body, err := handler.entityBody(view, units)
	if err != nil {
		return nil, handler.internalAPIError(ctx, "get-entity", "mapping", err)
	}
	return &GetEntityOutput{Body: body}, nil
}
