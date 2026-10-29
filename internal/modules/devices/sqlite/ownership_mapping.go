package sqlite

import (
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func ownedMappingFromRow(row dbsqlc.ListOwnedMappingsRow) devices.OwnedMapping {
	return devices.OwnedMapping{
		BindingKey: row.BindingKey,
		DeviceID:   devices.DeviceID(row.DeviceID),
		EntityKey:  row.EntityKey,
		EntityID:   devices.EntityID(row.EntityID),
	}
}
