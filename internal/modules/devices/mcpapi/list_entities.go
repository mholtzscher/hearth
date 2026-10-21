package mcpapi

import (
	"context"
	"errors"

	mcptools "github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
)

func registerListEntities(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[ListEntitiesInput, Page[EntitySummary]]{
			Name:         "list_entities",
			Description:  "List compact Entity summaries; use get_entity for support and diagnostic evidence.",
			OutputSchema: entityPageSchema(),
			ExactOutput:  true,
			Handler:      h.listEntities,
		},
	)
}

type ListEntitiesInput struct {
	pageInput

	DeviceID *deviceID `json:"device_id,omitempty" jsonschema:"canonical Hearth Device ID; omit to list every Device"`
}

func (handler *handler) listEntities(ctx context.Context, input ListEntitiesInput) (Page[EntitySummary], error) {
	var deviceID *devices.DeviceID
	if input.DeviceID != nil {
		value := devices.DeviceID(*input.DeviceID)
		deviceID = &value
	}
	var afterID *devices.EntityID
	if input.Cursor != "" {
		parsed, err := apicursor.DecodeEntitiesCursor(input.Cursor, deviceID)
		if err != nil {
			return Page[EntitySummary]{}, invalidRequestFailure("invalid cursor")
		}
		afterID = parsed
	}
	page, err := handler.devices.ListEntities(ctx, devices.ListEntitiesParams{
		DeviceID: deviceID, AfterID: afterID, Limit: pageSize(input.Limit),
	})
	if errors.Is(err, devices.ErrInvalidPage) {
		return Page[EntitySummary]{}, invalidRequestFailure("invalid page")
	}
	if err != nil {
		return Page[EntitySummary]{}, mcptools.InternalToolError(err)
	}
	body := Page[EntitySummary]{Items: make([]EntitySummary, len(page.Items))}
	for index, entity := range page.Items {
		body.Items[index] = entitySummary(entity)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeEntitiesCursor(page.Items[len(page.Items)-1].Entity.ID, deviceID)
		if cursorErr != nil {
			return Page[EntitySummary]{}, mcptools.InternalToolError(cursorErr)
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

func entityPageSchema() map[string]any { return pageSchema(entitySummarySchema()) }
