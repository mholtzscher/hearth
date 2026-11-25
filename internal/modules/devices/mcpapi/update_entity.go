package mcpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerUpdateEntity(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[UpdateEntityInput, EntityReceipt]{
			Name: "update_entity",
			Description: "Update an Entity's household metadata: enablement, name override, Area and tags. " +
				"Omitted fields are unchanged; null name_override restores the Adapter name; null area_id " +
				"makes the Entity inherit its Device's Area; tag_ids replaces the Entity's own tags.",
			Handler: h.updateEntity,
		},
	)
}

type UpdateEntityInput struct {
	EntityID     entityID  `json:"entity_id"               doc:"canonical Hearth Entity ID"`
	Enabled      *bool     `json:"enabled,omitempty"       doc:"whether the Entity accepts Commands"`
	NameOverride *string   `json:"name_override,omitempty" doc:"display name of 1 to 128 code points after trimming; null restores the Adapter-supplied name" nullable:"true"`
	AreaID       *labelID  `json:"area_id,omitempty"       doc:"canonical Label ID of an Area; null makes the Entity inherit its Device's Area"               nullable:"true"`
	TagIDs       []labelID `json:"tag_ids,omitempty"       doc:"canonical Label IDs of tags that replace the Entity's own tags; [] clears them"               nullable:"false"`

	// Presence distinguishes an omitted nullable field from an explicit null.
	nameOverridePresent, areaPresent bool
}

func (input *UpdateEntityInput) UnmarshalJSON(data []byte) error {
	type arguments UpdateEntityInput
	var decoded arguments
	present, err := decodePresentArguments(data, &decoded)
	if err != nil {
		return err
	}
	*input = UpdateEntityInput(decoded)
	input.nameOverridePresent, input.areaPresent = present["name_override"], present["area_id"]
	return nil
}

// EntityReceipt reports the committed naming, enablement, and Label fields.
type EntityReceipt struct {
	EntityID     string         `json:"entity_id"`
	Name         string         `json:"name"`
	AdapterName  string         `json:"adapter_name"`
	NameOverride *string        `json:"name_override" nullable:"true"`
	Enabled      bool           `json:"enabled"`
	Area         *EntityArea    `json:"area"`
	Tags         []LabelSummary `json:"tags"          nullable:"false"`
}

func (EntityReceipt) TransformSchema(registry huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Properties["area"] = nullableSchema[EntityArea](registry)
	return schema
}

func (h *handler) updateEntity(ctx context.Context, input UpdateEntityInput) (EntityReceipt, error) {
	id := devices.EntityID(input.EntityID)
	patch := devices.EntityPatch{Enabled: input.Enabled}
	if input.nameOverridePresent {
		patch.NameEdit = &devices.NameEdit{Override: input.NameOverride}
	}
	if input.areaPresent {
		edit := &devices.AreaEdit{}
		if input.AreaID != nil {
			area := devices.LabelID(*input.AreaID)
			edit.AreaID = &area
		}
		patch.AreaEdit = edit
	}
	if input.TagIDs != nil {
		patch.TagEdit = &devices.TagEdit{TagIDs: labelIDs(input.TagIDs)}
	}
	var view devices.EntityWithState
	var err error
	if patch == (devices.EntityPatch{}) {
		view, err = h.devices.GetEntity(ctx, id)
	} else {
		view, err = h.devices.PatchEntity(ctx, id, patch)
	}
	if err != nil {
		return EntityReceipt{}, updateFailure(err)
	}
	return EntityReceipt{
		EntityID:     string(view.Entity.ID),
		Name:         view.Entity.Name,
		AdapterName:  view.Entity.AdapterName,
		NameOverride: view.Entity.NameOverride,
		Enabled:      view.Entity.Enabled,
		Area:         entityArea(view.Labels.Area),
		Tags:         labelSummaries(view.Labels.Tags),
	}, nil
}
