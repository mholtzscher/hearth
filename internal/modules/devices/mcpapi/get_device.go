//nolint:goconst // Literal wire keys keep the fixed success contracts readable.
package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerGetDevice(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[GetDeviceInput, DeviceDetail]{
			Name:          "get_device",
			Description:   "Inspect a Device with a scoped page of Entity summaries.",
			SuccessSchema: deviceDetailSchema(),
			Handler:       h.getDevice,
		},
	)
}

type GetDeviceInput struct {
	DeviceID     deviceID  `json:"device_id"               jsonschema:"canonical Hearth Device ID"`
	EntityLimit  pageLimit `json:"entity_limit,omitempty"  jsonschema:"embedded Entity page size from 1 to 200; defaults to 50"`
	EntityCursor string    `json:"entity_cursor,omitempty" jsonschema:"opaque cursor from a previous page's next_entity_cursor"`
}

type DeviceDetail struct {
	DeviceID         string          `json:"device_id"`
	Name             string          `json:"name"`
	Kind             string          `json:"kind"`
	Entities         []EntitySummary `json:"entities"`
	NextEntityCursor *string         `json:"next_entity_cursor,omitempty"`
}

func (h *handler) getDevice(ctx context.Context, input GetDeviceInput) (DeviceDetail, error) {
	id := devices.DeviceID(input.DeviceID)
	params := devices.GetDeviceParams{ID: id, EntityLimit: pageSize(input.EntityLimit)}
	if input.EntityCursor != "" {
		after, err := apicursor.DecodeDeviceEntitiesCursor(input.EntityCursor, id)
		if err != nil {
			return DeviceDetail{}, invalidRequestFailure("invalid entity cursor")
		}
		params.AfterEntityID = after
	}
	aggregate, err := h.devices.GetDevice(ctx, params)
	if err != nil {
		return DeviceDetail{}, readFailure(err)
	}
	result := DeviceDetail{
		DeviceID: string(aggregate.Device.ID),
		Name:     aggregate.Device.Name,
		Kind:     string(aggregate.Device.Kind),
		Entities: make([]EntitySummary, len(aggregate.Entities.Items)),
	}
	for i, entity := range aggregate.Entities.Items {
		result.Entities[i] = entitySummary(entity)
	}
	if aggregate.Entities.HasMore && len(aggregate.Entities.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeDeviceEntitiesCursor(
			aggregate.Entities.Items[len(aggregate.Entities.Items)-1].Entity.ID,
			id,
		)
		if cursorErr != nil {
			return DeviceDetail{}, mcptools.InternalToolError(cursorErr)
		}
		result.NextEntityCursor = &cursor
	}
	return result, nil
}

func deviceDetailSchema() map[string]any {
	return recordSchema(map[string]any{
		"device_id": stringSchema(),
		"name":      stringSchema(),
		"kind":      stringSchema(),
		"entities": map[string]any{
			"type":  "array",
			"items": entitySummarySchema(),
		},
		"next_entity_cursor": stringSchema(),
	}, "device_id", "name", "kind", "entities")
}

func (input *GetDeviceInput) UnmarshalJSON(data []byte) error {
	type arguments GetDeviceInput
	var decoded arguments
	if err := decodeArguments(data, &decoded); err != nil {
		return err
	}
	*input = GetDeviceInput(decoded)
	return nil
}
