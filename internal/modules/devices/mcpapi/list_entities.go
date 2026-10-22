package mcpapi

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerListEntities(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[ListEntitiesInput, entityPage]{
			Name:        "list_entities",
			Description: "List compact Entity summaries; use get_entity for support and diagnostic evidence.",
			Handler:     h.listEntities,
		},
	)
}

// Entity pages have non-null collections/cursors, unlike the ordinary pages.
type entityPage struct {
	Items      []EntitySummary `json:"items"                 nullable:"false"`
	NextCursor *string         `json:"next_cursor,omitempty"`
}

type ListEntitiesInput struct {
	PageInput

	DeviceID *deviceID `json:"device_id,omitempty" doc:"canonical Hearth Device ID; omit to list every Device"`
}

//nolint:goconst // Preserve the advertised optional ID union locally.
func (ListEntitiesInput) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Properties["device_id"] = &huma.Schema{
		Description: schema.Properties["device_id"].Description,
		AnyOf:       []*huma.Schema{{Type: "null"}, {Type: "string"}},
	}
	return schema
}

func (handler *handler) listEntities(ctx context.Context, input ListEntitiesInput) (entityPage, error) {
	var deviceID *devices.DeviceID
	if input.DeviceID != nil {
		value := devices.DeviceID(*input.DeviceID)
		deviceID = &value
	}
	var afterID *devices.EntityID
	if input.Cursor != "" {
		parsed, err := apicursor.DecodeEntitiesCursor(input.Cursor, deviceID)
		if err != nil {
			return entityPage{}, invalidRequestFailure("invalid cursor")
		}
		afterID = parsed
	}
	page, err := handler.devices.ListEntities(ctx, devices.ListEntitiesParams{
		DeviceID: deviceID, AfterID: afterID, Limit: pageSize(input.Limit),
	})
	if errors.Is(err, devices.ErrInvalidPage) {
		return entityPage{}, invalidRequestFailure("invalid page")
	}
	if err != nil {
		return entityPage{}, mcptools.InternalToolError(err)
	}
	body := entityPage{Items: make([]EntitySummary, len(page.Items))}
	for index, entity := range page.Items {
		body.Items[index] = entitySummary(entity)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeEntitiesCursor(page.Items[len(page.Items)-1].Entity.ID, deviceID)
		if cursorErr != nil {
			return entityPage{}, mcptools.InternalToolError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return body, nil
}

func (input *ListEntitiesInput) UnmarshalJSON(data []byte) error {
	type arguments ListEntitiesInput
	var decoded arguments
	if err := decodeArguments(data, &decoded); err != nil {
		return err
	}
	*input = ListEntitiesInput(decoded)
	return nil
}
