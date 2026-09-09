package zigbee2mqtt //nolint:testpackage // Tests exercise shared power planning across light and relay families.

import (
	"encoding/json"
	"reflect"
	"testing"
)

func powerPlanningInput(device upstreamDevice) devicePlanningInput {
	return devicePlanningInput{IEEE: device.IEEEAddress, Exposes: newExposeIndex(device)}
}

func powerPlanByKey(t *testing.T, contribution plannerContribution, key string) entityPlan {
	t.Helper()
	for _, entity := range contribution.Entities {
		if entity.Descriptor.Key == key {
			return entity
		}
	}
	t.Fatalf("power Entity %q missing in %v", key, entityKeys(contribution.Entities))
	return entityPlan{}
}

// This test protects shared power identity and fails if light power and relay
// power diverge on key, name, external ID, or State routing for the same IEEE
// address and endpoint.
func TestPowerPlanningSharedIdentityAcrossFamilies(t *testing.T) {
	t.Parallel()
	ieee := "0x00124b0024abcdef"
	for _, scoped := range []struct {
		name          string
		light         upstreamDevice
		relay         upstreamDevice
		key           string
		display       string
		externalID    string
		stateProperty string
	}{
		{
			name:          "unscoped root",
			light:         eligibleDevice(),
			relay:         eligibleRelayDevice(),
			key:           "power",
			display:       "Power",
			externalID:    ieee + "/root/power",
			stateProperty: "state",
		},
		{
			name: "endpoint-scoped root",
			light: func() upstreamDevice {
				device := eligibleDevice()
				device.Endpoints = map[string]upstreamEndpoint{"1": {Name: "left"}}
				device.Definition.Exposes = []upstreamExpose{lightExpose("left", "state_left", "brightness_left")}
				return device
			}(),
			relay: func() upstreamDevice {
				device := eligibleRelayDevice()
				device.Endpoints = map[string]upstreamEndpoint{"1": {Name: "left"}}
				device.Definition.Exposes = []upstreamExpose{switchExpose("left", "state_left")}
				return device
			}(),
			key:           "power-ep1",
			display:       "left Power",
			externalID:    ieee + "/ep1/power",
			stateProperty: "state_left",
		},
	} {
		t.Run(scoped.name, func(t *testing.T) {
			t.Parallel()
			lightPower := powerPlanByKey(t, planLightFamily(powerPlanningInput(scoped.light)), scoped.key)
			relayPower := powerPlanByKey(t, planRelayFamily(powerPlanningInput(scoped.relay)), scoped.key)
			for _, power := range []entityPlan{lightPower, relayPower} {
				if power.Descriptor.Key != scoped.key || power.Descriptor.Name != scoped.display ||
					power.Descriptor.ExternalID != scoped.externalID ||
					power.Descriptor.Type != "hearth.power/v1" {
					t.Fatalf("power descriptor = %#v, want key %q name %q external ID %q",
						power.Descriptor, scoped.key, scoped.display, scoped.externalID)
				}
				if !reflect.DeepEqual(power.StateProperties, []string{scoped.stateProperty}) ||
					!reflect.DeepEqual(power.GetProperties, []string{scoped.stateProperty}) ||
					power.TranslateCommand == nil {
					t.Fatalf("power routes = %#v, want State property %q", power, scoped.stateProperty)
				}
			}
			if !reflect.DeepEqual(lightPower.Descriptor, relayPower.Descriptor) {
				t.Fatalf("light power descriptor = %#v, relay = %#v, want identical identity",
					lightPower.Descriptor, relayPower.Descriptor)
			}
		})
	}
}

// This test protects shared on/off distinction and fails if either family
// accepts indistinguishable value_on/value_off scalars, including numerics
// that differ textually but share one canonical value.
func TestPowerPlanningRejectsIndistinguishableOnOffInBothFamilies(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		valueOn  json.RawMessage
		valueOff json.RawMessage
	}{
		{name: "duplicate strings", valueOn: json.RawMessage(`"ON"`), valueOff: json.RawMessage(`"ON"`)},
		{name: "canonical numeric equivalents", valueOn: json.RawMessage(`1`), valueOff: json.RawMessage(`1.0`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lightDevice := eligibleDevice()
			lightDevice.Definition.Exposes[0].Features[0].ValueOn = test.valueOn
			lightDevice.Definition.Exposes[0].Features[0].ValueOff = test.valueOff
			lightContribution := planLightFamily(powerPlanningInput(lightDevice))
			if len(lightContribution.Entities) != 0 {
				t.Fatalf(
					"light power planned with indistinguishable on/off: %v",
					entityKeys(lightContribution.Entities),
				)
			}
			relayDevice := eligibleRelayDevice()
			relayDevice.Definition.Exposes[0].Features[0].ValueOn = test.valueOn
			relayDevice.Definition.Exposes[0].Features[0].ValueOff = test.valueOff
			relayContribution := planRelayFamily(powerPlanningInput(relayDevice))
			if len(relayContribution.Entities) != 0 {
				t.Fatalf(
					"relay power planned with indistinguishable on/off: %v",
					entityKeys(relayContribution.Entities),
				)
			}
		})
	}
}

// This test protects shared unique-property ownership and fails if either
// family claims a State property that a second Device expose also claims.
func TestPowerPlanningRequiresUniquePropertyInBothFamilies(t *testing.T) {
	t.Parallel()
	colliding := upstreamExpose{Type: "numeric", Name: "diagnostic", Property: "state", Access: 1}
	lightDevice := eligibleDevice()
	lightDevice.Definition.Exposes = append(lightDevice.Definition.Exposes, colliding)
	lightContribution := planLightFamily(powerPlanningInput(lightDevice))
	if len(lightContribution.Entities) != 0 {
		t.Fatalf("light power planned with colliding property: %v", entityKeys(lightContribution.Entities))
	}
	relayDevice := eligibleRelayDevice()
	relayDevice.Definition.Exposes = append(relayDevice.Definition.Exposes, colliding)
	relayContribution := planRelayFamily(powerPlanningInput(relayDevice))
	if len(relayContribution.Entities) != 0 {
		t.Fatalf("relay power planned with colliding property: %v", entityKeys(relayContribution.Entities))
	}
}

// This test protects distinct family gating over one shared power eligibility
// and fails if invalid light power keeps siblings or if one invalid relay
// root suppresses an independent valid sibling.
func TestPowerPlanningFamilyGatingDiffers(t *testing.T) {
	t.Parallel()
	t.Run("light power gates the whole family", func(t *testing.T) {
		t.Parallel()
		device := eligibleDevice()
		device.Definition.Exposes[0].Features[0].Access = 3
		device.Definition.Exposes = append(device.Definition.Exposes, upstreamExpose{
			Type: "enum", Name: "power_on_behavior", Property: "power_on_behavior", Access: 7,
			Values: []string{"off", "on", "toggle", "previous"},
		})
		lightContribution := planLightFamily(powerPlanningInput(device))
		if len(lightContribution.Entities) != 0 {
			t.Fatalf("light family survived invalid power: %v", entityKeys(lightContribution.Entities))
		}
	})
	t.Run("relay skips only the invalid root", func(t *testing.T) {
		t.Parallel()
		device := eligibleRelayDevice()
		device.Endpoints = map[string]upstreamEndpoint{"1": {Name: "left"}, "2": {Name: "right"}}
		invalid := switchExpose("left", "state_left")
		invalid.Features[0].Access = 3
		device.Definition.Exposes = []upstreamExpose{
			invalid,
			switchExpose("right", "state_right"),
			switchExpose("missing", "state_missing"),
		}
		contribution := planRelayFamily(powerPlanningInput(device))
		if got := entityKeys(contribution.Entities); !reflect.DeepEqual(got, []string{"power-ep2"}) {
			t.Fatalf("relay Entity keys = %v, want only the valid scoped sibling", got)
		}
		if got := contribution.Entities[0].StateProperties; !reflect.DeepEqual(got, []string{"state_right"}) {
			t.Fatalf("relay survivor State properties = %v, want state_right", got)
		}
	})
}
