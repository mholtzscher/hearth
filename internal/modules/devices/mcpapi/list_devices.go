package mcpapi

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerListDevices(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[ListDevicesInput, Page[DeviceSummary]]{
			Name:        "list_devices",
			Description: "List Device summaries; use get_device for a page of Entity summaries.",
			Handler:     h.listDevices,
		},
	)
}

type ListDevicesInput struct {
	PageInput
}

func (handler *handler) listDevices(ctx context.Context, input ListDevicesInput) (Page[DeviceSummary], error) {
	var afterID *devices.DeviceID
	if input.Cursor != "" {
		parsed, err := apicursor.DecodeDevicesCursor(input.Cursor)
		if err != nil {
			return Page[DeviceSummary]{}, invalidRequestFailure("invalid cursor")
		}
		afterID = parsed
	}
	page, err := handler.devices.ListDevices(
		ctx,
		devices.ListDevicesParams{AfterID: afterID, Limit: pageSize(input.Limit)},
	)
	if errors.Is(err, devices.ErrInvalidPage) {
		return Page[DeviceSummary]{}, invalidRequestFailure("invalid page")
	}
	if err != nil {
		return Page[DeviceSummary]{}, mcptools.InternalToolError(err)
	}
	body := Page[DeviceSummary]{Items: make([]DeviceSummary, len(page.Items))}
	for index, device := range page.Items {
		body.Items[index] = DeviceSummary{
			DeviceID: string(device.ID), Name: device.Name, Kind: string(device.Kind),
			Area: deviceArea(device.Labels.Area), Tags: labelSummaries(device.Labels.Tags),
		}
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeDevicesCursor(page.Items[len(page.Items)-1].ID)
		if cursorErr != nil {
			return Page[DeviceSummary]{}, mcptools.InternalToolError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return body, nil
}

type DeviceSummary struct {
	DeviceID string         `json:"device_id"`
	Name     string         `json:"name"`
	Kind     string         `json:"kind"`
	Area     *LabelSummary  `json:"area"`
	Tags     []LabelSummary `json:"tags"      nullable:"false"`
}

func (DeviceSummary) TransformSchema(registry huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Properties["area"] = nullableSchema[LabelSummary](registry)
	return schema
}
