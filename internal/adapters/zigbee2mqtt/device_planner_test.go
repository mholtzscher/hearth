package zigbee2mqtt //nolint:testpackage // Tests exercise package-private planners and device merge.

import (
	"encoding/json"
	"reflect"
	"testing"
)

func switchExpose(endpoint, property string) upstreamExpose {
	return upstreamExpose{
		Type: "switch", Endpoint: endpoint,
		Features: []upstreamExpose{{
			Type: "binary", Name: "state", Property: property, Access: 7,
			ValueOn: json.RawMessage(`"ON"`), ValueOff: json.RawMessage(`"OFF"`),
		}},
	}
}

func eligibleSensorDevice(property string, access int) upstreamDevice {
	return upstreamDevice{
		IEEEAddress: "0x00124b0024abcdef", Type: "Router", Supported: true,
		FriendlyName: "test-sensor", InterviewState: "SUCCESSFUL",
		Endpoints: map[string]upstreamEndpoint{},
		Definition: &upstreamDefinition{
			Model: "TEST", Vendor: "Fixture", Description: "Fixture",
			Exposes: []upstreamExpose{{
				Type: "numeric", Name: "temperature", Property: property, Access: access, Unit: "°C",
			}},
		},
	}
}

func eligibleRelayDevice() upstreamDevice {
	device := eligibleDevice()
	device.FriendlyName = "test-relay"
	device.Definition.Exposes = []upstreamExpose{switchExpose("", "state")}
	return device
}

// This test protects primary-family precedence and fails if a Device exposing
// both light and switch shapes registers anything other than the light family.
func TestPlanDeviceLightWinsOverRelay(t *testing.T) {
	t.Parallel()
	device := eligibleDevice()
	device.Definition.Exposes = append(device.Definition.Exposes, switchExpose("", "state_switch"))
	discovered, rejection := discoverDevice(device, mustEmbeddedProfileCatalog(t))
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if discovered.Registration.Device.Kind != "light" {
		t.Fatalf("Device kind = %q, want light", discovered.Registration.Device.Kind)
	}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"power", "brightness"}) {
		t.Fatalf("Entity keys = %v", got)
	}
}

// This test protects the relay proof Device and fails on hard-coded ON/OFF,
// a relay-specific Device kind, or power identity that diverges from lights.
func TestPlanDeviceRelayRegistersSharedPower(t *testing.T) {
	t.Parallel()
	discovered, rejection := discoverDevice(eligibleRelayDevice(), mustEmbeddedProfileCatalog(t))
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if discovered.Registration.BindingKey != "z2m-00124b0024abcdef" ||
		discovered.Registration.Device.Kind != "relay" {
		t.Fatalf("Device identity = %#v", discovered.Registration)
	}
	if len(discovered.Entities) != 1 {
		t.Fatalf("Entities = %#v", discovered.Entities)
	}
	power := discovered.Entities[0]
	if power.Descriptor.Key != "power" || power.Descriptor.ExternalID != "0x00124b0024abcdef/root/power" ||
		power.Descriptor.Name != "Power" || power.Descriptor.Type != "hearth.power/v1" {
		t.Fatalf("power descriptor = %#v", power.Descriptor)
	}
	if !reflect.DeepEqual(power.StateProperties, []string{"state"}) ||
		!reflect.DeepEqual(power.GetProperties, []string{"state"}) || power.TranslateCommand == nil {
		t.Fatalf("power plan = %#v", power)
	}
}

// This test protects endpoint-scoped relay identity and fails if numeric
// endpoints or endpoint labels change canonical power keys.
func TestPlanDeviceRelayEndpointIdentity(t *testing.T) {
	t.Parallel()
	device := eligibleRelayDevice()
	device.Endpoints = map[string]upstreamEndpoint{"1": {Name: "left"}}
	device.Definition.Exposes = []upstreamExpose{switchExpose("left", "state_left")}
	discovered, rejection := discoverDevice(device, mustEmbeddedProfileCatalog(t))
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if len(discovered.Entities) != 1 {
		t.Fatalf("Entities = %#v", discovered.Entities)
	}
	power := discovered.Entities[0]
	if power.Descriptor.Key != "power-ep1" || power.Descriptor.Name != "left Power" ||
		power.Descriptor.ExternalID != "0x00124b0024abcdef/ep1/power" {
		t.Fatalf("endpoint power descriptor = %#v", power.Descriptor)
	}
	if !reflect.DeepEqual(power.StateProperties, []string{"state_left"}) {
		t.Fatalf("endpoint power properties = %v", power.StateProperties)
	}
}

// This test protects endpoint-scoped temperature identity and fails if the
// sensor planner ignores endpoint labels or custom property names.
func TestPlanDeviceSensorEndpointIdentity(t *testing.T) {
	t.Parallel()
	device := eligibleSensorDevice("temperature_right", 1|4)
	device.Endpoints = map[string]upstreamEndpoint{"2": {Name: "right"}}
	device.Definition.Exposes[0].Endpoint = "right"
	discovered, rejection := discoverDevice(device, mustEmbeddedProfileCatalog(t))
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if len(discovered.Entities) != 1 {
		t.Fatalf("Entities = %#v", discovered.Entities)
	}
	temperature := discovered.Entities[0]
	if temperature.Descriptor.Key != "temperature-ep2" || temperature.Descriptor.Name != "right Temperature" ||
		temperature.Descriptor.ExternalID != "0x00124b0024abcdef/ep2/temperature" {
		t.Fatalf("endpoint temperature descriptor = %#v", temperature.Descriptor)
	}
	if !reflect.DeepEqual(temperature.StateProperties, []string{"temperature_right"}) ||
		!reflect.DeepEqual(temperature.GetProperties, []string{"temperature_right"}) {
		t.Fatalf("endpoint temperature plan = %#v", temperature)
	}
}

// This test protects mixed relay and temperature merge and fails if one IEEE
// address produces anything other than one relay Device with both Entities.
func TestPlanDeviceRelayWithTemperatureMergesOneDevice(t *testing.T) {
	t.Parallel()
	device := eligibleRelayDevice()
	device.Definition.Exposes = append(device.Definition.Exposes,
		eligibleSensorDevice("temperature", 1).Definition.Exposes...)
	discovered, rejection := discoverDevice(device, mustEmbeddedProfileCatalog(t))
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if discovered.Registration.Device.Kind != "relay" {
		t.Fatalf("Device kind = %q, want relay", discovered.Registration.Device.Kind)
	}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"power", "temperature"}) {
		t.Fatalf("Entity keys = %v", got)
	}
}

// This test protects supplemental sensors on lights and fails if temperature
// changes the primary family or disturbs existing light identity.
func TestPlanDeviceLightWithTemperatureKeepsLight(t *testing.T) {
	t.Parallel()
	device := eligibleDevice()
	device.Definition.Exposes = append(device.Definition.Exposes,
		eligibleSensorDevice("temperature", 1).Definition.Exposes...)
	discovered, rejection := discoverDevice(device, mustEmbeddedProfileCatalog(t))
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if discovered.Registration.Device.Kind != "light" {
		t.Fatalf("Device kind = %q, want light", discovered.Registration.Device.Kind)
	}
	want := []string{"power", "brightness", "temperature"}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, want) {
		t.Fatalf("Entity keys = %v, want %v", got, want)
	}
	if externalID := discovered.Entities[0].Descriptor.ExternalID; externalID != "0x00124b0024abcdef/root/power" {
		t.Fatalf("power external ID = %q", externalID)
	}
}

// This test protects family-aware rejection codes and fails if a switch-only
// or sensor-less Device collapses to the light diagnostic.
func TestPlanDeviceRejectionCodesFollowStrongestRoot(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		device upstreamDevice
		code   string
	}{
		{
			name: "light root without power",
			device: func() upstreamDevice {
				device := eligibleDevice()
				device.Definition.Exposes[0].Features[0].Access = 3
				return device
			}(),
			code: rejectionNoEligibleLight,
		},
		{
			name: "switch root without power",
			device: func() upstreamDevice {
				device := eligibleDevice()
				device.Definition.Exposes = []upstreamExpose{switchExpose("", "state")}
				device.Definition.Exposes[0].Features[0].Access = 3
				return device
			}(),
			code: rejectionNoEligibleRelay,
		},
		{
			name: "unrelated roots",
			device: func() upstreamDevice {
				device := eligibleDevice()
				device.Definition.Exposes = []upstreamExpose{{
					Type: "numeric", Name: "diagnostic", Property: "diagnostic", Access: 1,
				}}
				return device
			}(),
			code: rejectionNoEligibleEntity,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, rejection := discoverDevice(test.device, mustEmbeddedProfileCatalog(t))
			if rejection == nil || rejection.Code != test.code {
				t.Fatalf("rejection = %#v, want code %q", rejection, test.code)
			}
		})
	}
}

// This test protects same-family key disambiguation and fails if duplicated
// switch roots keep an ambiguous power route.
func TestPlanDeviceDropsDuplicatedRelayKeys(t *testing.T) {
	t.Parallel()
	device := eligibleRelayDevice()
	device.Definition.Exposes = []upstreamExpose{switchExpose("", "state_a"), switchExpose("", "state_b")}
	_, rejection := discoverDevice(device, mustEmbeddedProfileCatalog(t))
	if rejection == nil || rejection.Code != rejectionNoEligibleRelay {
		t.Fatalf("rejection = %#v, want %q", rejection, rejectionNoEligibleRelay)
	}
}

// This test protects whole-root light dedup and fails if an orphaned optional
// brightness survives when two unscoped roots collide on one power key. Only
// the first root carries brightness, so per-key dedup would keep a lone
// brightness without eligible power.
func TestPlanDeviceDropsAsymmetricDuplicateLightRoots(t *testing.T) {
	t.Parallel()
	device := eligibleDevice()
	first := lightExpose("", "state_a", "brightness_a")
	second := lightExpose("", "state_b", "")
	second.Features = second.Features[:1]
	device.Definition.Exposes = []upstreamExpose{first, second}
	discovered, rejection := discoverDevice(device, mustEmbeddedProfileCatalog(t))
	if rejection == nil || rejection.Code != rejectionNoEligibleLight {
		t.Fatalf("discovered = %#v, rejection = %#v, want %q", discovered, rejection, rejectionNoEligibleLight)
	}
	if discovered.Entities != nil {
		for _, entity := range discovered.Entities {
			if entity.Descriptor.Key == "brightness" {
				t.Fatalf("orphaned brightness survived duplicate power roots: %#v", discovered.Entities)
			}
		}
	}
}

// This test protects canonical identity across restart and friendly-name
// changes. It fails if mutable routing metadata or MQTT property names leak
// into Binding or Entity keys.
func TestPlanDeviceKeysIgnoreMutableMetadata(t *testing.T) {
	t.Parallel()
	first := eligibleRelayDevice()
	second := eligibleRelayDevice()
	second.FriendlyName = "renamed-relay"
	second.Description = "Renamed Relay"
	second.Definition.Exposes = []upstreamExpose{switchExpose("", "renamed_state")}
	firstDiscovered, firstRejection := discoverDevice(first, mustEmbeddedProfileCatalog(t))
	secondDiscovered, secondRejection := discoverDevice(second, mustEmbeddedProfileCatalog(t))
	if firstRejection != nil || secondRejection != nil {
		t.Fatalf("rejections = %#v, %#v", firstRejection, secondRejection)
	}
	if firstDiscovered.Registration.BindingKey != secondDiscovered.Registration.BindingKey ||
		!reflect.DeepEqual(entityKeys(firstDiscovered.Entities), entityKeys(secondDiscovered.Entities)) {
		t.Fatalf("identity changed: %#v vs %#v", firstDiscovered.Registration, secondDiscovered.Registration)
	}
}
