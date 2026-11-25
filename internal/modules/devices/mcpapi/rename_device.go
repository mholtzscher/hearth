package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerRenameDevice(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[RenameDeviceInput, DeviceNameReceipt]{
			Name: "rename_device",
			Description: "Rename a Device by setting its household name override, or pass null to restore " +
				"the Adapter-supplied name. Identity and the external device are unchanged.",
			Handler: h.renameDevice,
		},
	)
}

type RenameDeviceInput struct {
	DeviceID     deviceID `json:"device_id"     doc:"canonical Hearth Device ID"`
	NameOverride *string  `json:"name_override" doc:"display name of 1 to 128 code points after trimming; null restores the Adapter-supplied name" nullable:"true"`
}

// DeviceNameReceipt reports the committed naming fields: the effective display
// name, the latest Adapter-supplied name, and the override, if any.
type DeviceNameReceipt struct {
	DeviceID     string  `json:"device_id"`
	Name         string  `json:"name"`
	AdapterName  string  `json:"adapter_name"`
	NameOverride *string `json:"name_override" nullable:"true"`
}

func (h *handler) renameDevice(ctx context.Context, input RenameDeviceInput) (DeviceNameReceipt, error) {
	device, err := h.devices.PatchDevice(ctx, devices.DeviceID(input.DeviceID), devices.DevicePatch{
		NameEdit: &devices.NameEdit{Override: input.NameOverride},
	})
	if err != nil {
		return DeviceNameReceipt{}, renameFailure(err)
	}
	return DeviceNameReceipt{
		DeviceID:     string(device.ID),
		Name:         device.Name,
		AdapterName:  device.AdapterName,
		NameOverride: device.NameOverride,
	}, nil
}
