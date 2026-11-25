package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerRenameEntity(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[RenameEntityInput, EntityNameReceipt]{
			Name: "rename_entity",
			Description: "Rename an Entity by setting its household name override, or pass null to restore " +
				"the Adapter-supplied name. Identity and enablement are unchanged.",
			Handler: h.renameEntity,
		},
	)
}

type RenameEntityInput struct {
	EntityID     entityID `json:"entity_id"     doc:"canonical Hearth Entity ID"`
	NameOverride *string  `json:"name_override" doc:"display name of 1 to 128 code points after trimming; null restores the Adapter-supplied name" nullable:"true"`
}

// EntityNameReceipt reports the committed naming fields: the effective display
// name, the latest Adapter-supplied name, and the override, if any.
type EntityNameReceipt struct {
	EntityID     string  `json:"entity_id"`
	Name         string  `json:"name"`
	AdapterName  string  `json:"adapter_name"`
	NameOverride *string `json:"name_override" nullable:"true"`
}

func (h *handler) renameEntity(ctx context.Context, input RenameEntityInput) (EntityNameReceipt, error) {
	view, err := h.devices.PatchEntity(ctx, devices.EntityID(input.EntityID), devices.EntityPatch{
		NameEdit: &devices.NameEdit{Override: input.NameOverride},
	})
	if err != nil {
		return EntityNameReceipt{}, renameFailure(err)
	}
	return EntityNameReceipt{
		EntityID:     string(view.Entity.ID),
		Name:         view.Entity.Name,
		AdapterName:  view.Entity.AdapterName,
		NameOverride: view.Entity.NameOverride,
	}, nil
}
