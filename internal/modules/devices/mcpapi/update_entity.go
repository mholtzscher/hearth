package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerUpdateEntity(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[UpdateEntityInput, EntityReceipt]{
			Name:        "update_entity",
			Description: "Set Entity enablement; return the committed identity and enabled value.",
			Handler:     h.updateEntity,
		},
	)
}

type UpdateEntityInput struct {
	EntityID entityID `json:"entity_id" doc:"canonical Hearth Entity ID"`
	Enabled  bool     `json:"enabled"   doc:"whether the Entity accepts Commands"`
}

type EntityReceipt struct {
	EntityID string `json:"entity_id"`
	Enabled  bool   `json:"enabled"`
}

func (h *handler) updateEntity(ctx context.Context, input UpdateEntityInput) (EntityReceipt, error) {
	view, err := h.devices.SetEntityEnabled(ctx, devices.EntityID(input.EntityID), input.Enabled)
	if err != nil {
		return EntityReceipt{}, readFailure(err)
	}
	return EntityReceipt{EntityID: string(view.Entity.ID), Enabled: view.Entity.Enabled}, nil
}
