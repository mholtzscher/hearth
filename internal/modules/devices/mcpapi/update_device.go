package mcpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerUpdateDevice(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[UpdateDeviceInput, DeviceReceipt]{
			Name: "update_device",
			Description: "Update a Device's household metadata: name override, Area and tags. Omitted fields " +
				"are unchanged; null name_override restores the Adapter name; null area_id clears the Area; " +
				"tag_ids replaces the Device's tags. Identity and the external device are unchanged.",
			Handler: h.updateDevice,
		},
	)
}

type UpdateDeviceInput struct {
	DeviceID     deviceID  `json:"device_id"               doc:"canonical Hearth Device ID"`
	NameOverride *string   `json:"name_override,omitempty" doc:"display name of 1 to 128 code points after trimming; null restores the Adapter-supplied name" nullable:"true"`
	AreaID       *labelID  `json:"area_id,omitempty"       doc:"canonical Label ID of an Area; null clears the Device's Area"                                 nullable:"true"`
	TagIDs       []labelID `json:"tag_ids,omitempty"       doc:"canonical Label IDs of tags that replace the Device's tags; [] clears them"                   nullable:"false"`

	// Presence distinguishes an omitted nullable field from an explicit null.
	nameOverridePresent, areaPresent bool
}

func (input *UpdateDeviceInput) UnmarshalJSON(data []byte) error {
	type arguments UpdateDeviceInput
	var decoded arguments
	present, err := decodePresentArguments(data, &decoded)
	if err != nil {
		return err
	}
	*input = UpdateDeviceInput(decoded)
	input.nameOverridePresent, input.areaPresent = present["name_override"], present["area_id"]
	return nil
}

// DeviceReceipt reports the committed naming and Label fields.
type DeviceReceipt struct {
	DeviceID     string         `json:"device_id"`
	Name         string         `json:"name"`
	AdapterName  string         `json:"adapter_name"`
	NameOverride *string        `json:"name_override" nullable:"true"`
	Area         *LabelSummary  `json:"area"`
	Tags         []LabelSummary `json:"tags"          nullable:"false"`
}

func (DeviceReceipt) TransformSchema(registry huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Properties["area"] = nullableSchema[LabelSummary](registry)
	return schema
}

func (h *handler) updateDevice(ctx context.Context, input UpdateDeviceInput) (DeviceReceipt, error) {
	id := devices.DeviceID(input.DeviceID)
	patch := devices.DevicePatch{}
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
	var device devices.Device
	var err error
	if patch == (devices.DevicePatch{}) {
		var aggregate devices.DeviceAggregate
		aggregate, err = h.devices.GetDevice(ctx, devices.GetDeviceParams{ID: id, EntityLimit: 1})
		device = aggregate.Device
	} else {
		device, err = h.devices.PatchDevice(ctx, id, patch)
	}
	if err != nil {
		return DeviceReceipt{}, updateFailure(err)
	}
	return DeviceReceipt{
		DeviceID:     string(device.ID),
		Name:         device.Name,
		AdapterName:  device.AdapterName,
		NameOverride: device.NameOverride,
		Area:         deviceArea(device.Labels.Area),
		Tags:         labelSummaries(device.Labels.Tags),
	}, nil
}
