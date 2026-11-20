package homekit_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/adapters/homekit"
)

func TestDefaultAccessoryRolesUseCanonicalEntityTypes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		deviceKind string
		kind       homekit.AccessoryKind
		entities   []homekit.Entity
		roles      []homekit.RoleAssignment
	}{
		{"light", homekit.KindLightbulb,
			[]homekit.Entity{entity("level", "hearth.brightness/v1"), entity("power", "hearth.power/v1")},
			[]homekit.RoleAssignment{{EntityID: "power", Role: "on"}, {EntityID: "level", Role: "brightness"}}},
		{"lock", homekit.KindLock, []homekit.Entity{entity("lock", "hearth.lock/v1")},
			[]homekit.RoleAssignment{{EntityID: "lock", Role: "lock"}}},
		{"cover", homekit.KindWindowCovering, []homekit.Entity{entity("cover", "hearth.cover/v1")},
			[]homekit.RoleAssignment{{EntityID: "cover", Role: "position"}}},
		{"thermostat", homekit.KindThermostat, []homekit.Entity{entity("hvac", "hearth.thermostat/v1")},
			[]homekit.RoleAssignment{{EntityID: "hvac", Role: "thermostat"}}},
		{"sensor", homekit.KindSensor,
			[]homekit.Entity{entity("temp", "hearth.temperature/v1"), entity("humidity", "hearth.relativehumidity/v1")},
			[]homekit.RoleAssignment{{EntityID: "temp", Role: "temperature"}, {EntityID: "humidity", Role: "humidity"}}},
		{"relay", homekit.KindSwitch,
			[]homekit.Entity{entity("right", "hearth.power/v1"), entity("left", "hearth.power/v1")},
			[]homekit.RoleAssignment{{EntityID: "left", Role: "on"}, {EntityID: "right", Role: "on"}}},
	} {
		t.Run(tc.deviceKind, func(t *testing.T) {
			t.Parallel()
			device := homekit.Device{ID: "device", Kind: tc.deviceKind}
			proposal := homekit.Propose(device, tc.entities, "")
			assert.Equal(t, tc.kind, proposal.Kind)
			assert.Empty(t, proposal.Unresolved)
			assert.Equal(t, tc.roles, proposal.Assigned)
			require.NoError(t, homekit.ValidateExposure(homekit.Exposure{
				DeviceID: device.ID, Kind: proposal.Kind, Entities: proposal.Assigned,
			}, inventory(device, tc.entities)))
		})
	}
}

func TestAirPurifierRequiresExplicitControlAssignments(t *testing.T) {
	t.Parallel()
	device := homekit.Device{ID: "device", Kind: "air-purifier"}
	particles := entity("particles", "hearth.numericsensor/v1")
	particles.Support = json.RawMessage(`{"state":{"quantity_kind":"particulate_matter","unit":"µg/m³"}}`)
	filter := entity("filter", "hearth.numericsensor/v1")
	filter.Support = json.RawMessage(`{"state":{"unit":"%"}}`)
	entities := []homekit.Entity{
		entity("power", "hearth.power/v1"), entity("display", "hearth.power/v1"),
		entity("child-lock", "hearth.power/v1"), entity("mode", "hearth.enumsetting/v1"),
		entity("fan-level", "hearth.enumsetting/v1"), particles, filter,
	}
	proposal := homekit.Propose(device, entities, homekit.KindAirPurifier)
	assert.Equal(t, []homekit.RoleAssignment{
		{EntityID: "particles", Role: "pm25"}, {EntityID: "filter", Role: "filter_life"},
	}, proposal.Assigned)
	assert.Contains(t, proposal.Unresolved, homekit.UnresolvedRole{
		Role: "active", Reason: "ambiguous", Candidates: []string{"child-lock", "display", "power"},
	})
	assert.Contains(t, proposal.Unresolved, homekit.UnresolvedRole{
		Role: "mode", Reason: "ambiguous", Candidates: []string{"fan-level", "mode"},
	})
	require.NoError(t, homekit.ValidateExposure(homekit.Exposure{
		DeviceID: device.ID, Kind: homekit.KindAirPurifier,
		Entities: []homekit.RoleAssignment{{EntityID: "power", Role: "active"}, {EntityID: "fan-level", Role: "speed"}},
	}, inventory(device, entities)))
}

func TestBinarySensorRequiresClassAndRejectsAnEntityOnAnotherDevice(t *testing.T) {
	t.Parallel()
	device := homekit.Device{ID: "device", Kind: "sensor"}
	own := entity("input", "hearth.binarysensor/v1")
	other := entity("other-input", "hearth.binarysensor/v1")
	other.DeviceID = "other-device"
	entities := []homekit.Entity{other, own}
	proposal := homekit.Propose(device, entities, "")
	require.Empty(t, proposal.Assigned)
	require.Len(t, proposal.Unresolved, 1)
	assert.Equal(t, "class_required", proposal.Unresolved[0].Reason)
	assert.Equal(t, []string{"input"}, proposal.Unresolved[0].Candidates)

	exposure := homekit.Exposure{DeviceID: device.ID, Kind: proposal.Kind,
		Entities: []homekit.RoleAssignment{{EntityID: "input", Role: "contact", Inverted: true}},
	}
	inv := inventory(device, entities)
	require.NoError(t, homekit.ValidateExposure(exposure, inv))
	exposure.Entities[0].EntityID = other.ID
	var planErr *homekit.PlanError
	require.ErrorAs(t, homekit.ValidateExposure(exposure, inv), &planErr)
	assert.Equal(t, "entity_not_on_device", planErr.Reason)
}

func TestGarageCoverDoesNotAdvertiseGarageDoorServiceWithoutObstructionEvidence(t *testing.T) {
	t.Parallel()
	device := homekit.Device{ID: "device", Kind: "cover"}
	entities := []homekit.Entity{entity("position", "hearth.cover/v1")}
	proposal := homekit.Propose(device, entities, "")
	require.Equal(t, homekit.KindWindowCovering, proposal.Kind)
	err := homekit.ValidateExposure(homekit.Exposure{
		DeviceID: device.ID, Kind: homekit.AccessoryKind("garage_door"), Entities: proposal.Assigned,
	}, inventory(device, entities))
	var planErr *homekit.PlanError
	require.ErrorAs(t, err, &planErr)
	assert.Equal(t, "kind_incompatible", planErr.Reason)
}

func entity(id, typeID string) homekit.Entity {
	return homekit.Entity{ID: id, DeviceID: "device", Type: typeID}
}

func inventory(device homekit.Device, entities []homekit.Entity) homekit.Inventory {
	result := homekit.Inventory{
		Devices:  map[string]homekit.Device{device.ID: device},
		Entities: make(map[string]homekit.Entity),
	}
	for _, item := range entities {
		result.Entities[item.ID] = item
	}
	return result
}
