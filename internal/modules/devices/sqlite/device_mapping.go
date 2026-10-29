package sqlite

import (
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func deviceFromRow(row dbsqlc.GetDeviceRow) devices.Device {
	return devices.Device{
		ID:           devices.DeviceID(row.ID),
		Kind:         devices.DeviceKind(row.Kind),
		Name:         row.Name,
		AdapterName:  row.AdapterName,
		NameOverride: nameOverrideFromSQL(row.NameOverride),
	}
}
