//go:build integration || e2e

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func powerEntity() devices.EntityDescriptor {
	return devices.EntityDescriptor{
		Key: "power", ExternalID: "callback.power", Name: "Power",
		TypeID:  devices.EntityTypePowerV1,
		Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
	}
}
