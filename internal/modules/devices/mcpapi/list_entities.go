package mcpapi

import (
	"context"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerListEntities(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[ListEntitiesInput, entityPage]{
			Name:        "list_entities",
			Description: "List compact Entity summaries, optionally filtered by label_ids and types; use get_entity for support and diagnostic evidence.",
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

	DeviceID *deviceID `json:"device_id,omitempty" doc:"canonical Hearth Device ID; omit to list every Device"                                nullable:"true"`
	LabelIDs []labelID `json:"label_ids,omitempty" doc:"Entities must have every listed Label; an Entity inherits its Device's Area and tags" nullable:"false"`
	Types    []string  `json:"types,omitempty"     doc:"Entity must be one of the listed types"                                               nullable:"false"`
	Units    string    `json:"units,omitempty"     doc:"Omit for household units"                                                                              enum:",canonical"`
}

func (handler *handler) listEntities(ctx context.Context, input ListEntitiesInput) (entityPage, error) {
	units, err := devices.ParseReadUnits(input.Units)
	if err != nil {
		return entityPage{}, invalidRequestFailure("invalid units")
	}
	var deviceID *devices.DeviceID
	if input.DeviceID != nil {
		value := devices.DeviceID(*input.DeviceID)
		deviceID = &value
	}
	selector := devices.EntitySelector{Labels: labelIDs(input.LabelIDs)}
	for _, typeID := range input.Types {
		selector.Types = append(selector.Types, devices.EntityTypeID(typeID))
	}
	var afterID *devices.EntityID
	if input.Cursor != "" {
		parsed, cursorErr := apicursor.DecodeEntitiesCursor(input.Cursor, deviceID, selector)
		if cursorErr != nil {
			return entityPage{}, invalidRequestFailure("invalid cursor")
		}
		afterID = parsed
	}
	page, err := handler.devices.ListEntities(ctx, devices.ListEntitiesParams{
		DeviceID: deviceID, Selector: selector, AfterID: afterID, Limit: pageSize(input.Limit),
	})
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return entityPage{}, invalidRequestFailure("invalid page")
	case errors.Is(err, devices.ErrLabelNotFound):
		return entityPage{}, toolError(failureLabelNotFound, "label not found")
	case errors.Is(err, devices.ErrInvalidEntitySelector):
		return entityPage{}, toolError(failureInvalidSelector,
			"selector must list at most 8 distinct label_ids and 16 known entity types")
	}
	if err != nil {
		return entityPage{}, mcptools.InternalToolError(err)
	}
	body := entityPage{Items: make([]EntitySummary, len(page.Items))}
	for index, entity := range page.Items {
		body.Items[index], err = handler.entitySummary(entity, units)
		if err != nil {
			return entityPage{}, mcptools.InternalToolError(err)
		}
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeEntitiesCursor(
			page.Items[len(page.Items)-1].Entity.ID, deviceID, selector,
		)
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
