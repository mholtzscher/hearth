package httpapi

import "github.com/mholtzscher/hearth/internal/modules/devices"

func deviceBody(device devices.Device) DeviceBody {
	return DeviceBody{
		ID:           string(device.ID),
		Kind:         string(device.Kind),
		Name:         device.Name,
		AdapterName:  device.AdapterName,
		NameOverride: device.NameOverride,
	}
}
