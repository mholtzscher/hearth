package zigbee2mqtt //nolint:testpackage // Tests exercise package-private discovery, decoding, and planner order.

// Fixture provenance: testdata/bridge-devices-3rsnl02043z.json and
// testdata/state-3rsnl02043z.json are handcrafted minimal sanitized shapes
// modeled on the live Third Reality 3RSNL02043Z hallway night light capture
// from host wanda on 2026-09-11 (Zigbee2MQTT; model 3RSNL02043Z, vendor
// Third Reality, description "Zigbee multi-function night light",
// software_build_id v1.00.86). Sanitization changed the IEEE address,
// friendly name, and device description only. Model, vendor, build, expose
// nesting, expose type/name/property/access, units, enum values, binary
// value_on/value_off, numeric bounds, and the state payload values
// (illuminance 20, occupancy true) are retained from the live evidence. No
// endpoints cluster/database/network metadata and no real IEEE address or
// hardware are included.
//
// The captured Device discovers exactly nine Entities on a light Device in
// planner order: power, brightness, colorxy, colormode, poweronbehavior,
// effect, illuminance, occupancy, linkquality. Illuminance is the only new
// capability this capture adds: a root numeric expose named illuminance,
// property illuminance, unit lx, access 5 (publish+get), mapped to a
// read-only hearth.measurement/v1 Entity with a 0..1e9 lx validation
// envelope. Occupancy (access 1, value_on true, value_off false) supplies
// the binary sibling that must survive an invalid illuminance reading.

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const nightLightFixtureName = "bridge-devices-3rsnl02043z.json"

const nightLightIEEE = "0x00124b0024abcd05"

func mustNightLightDevice(t *testing.T) discoveredDevice {
	t.Helper()
	return mustDiscoveredFixtureDevice(t, nightLightFixtureName)
}

// nightLightPlanByKey returns the discovered plan for one Entity key.
func nightLightPlanByKey(t *testing.T, device discoveredDevice, key string) entityPlan {
	t.Helper()
	for _, entity := range device.Entities {
		if entity.Descriptor.Key == key {
			return entity
		}
	}
	t.Fatalf("Entity %q missing from %v", key, entityKeys(device.Entities))
	return entityPlan{}
}

// This test protects the captured 3RSNL02043Z discovery: the night light must
// stay a light Device with exactly the nine captured Entities in planner
// order and the literal descriptor identity and support of each. It fails if
// illuminance is dropped, reordered, mistyped, given a different unit or
// envelope, or if any sibling capability changes shape.
func TestDiscoverCapturedNightLight3RSNL02043Z(t *testing.T) {
	t.Parallel()
	device := mustNightLightDevice(t)
	if device.IEEEAddress != nightLightIEEE || device.FriendlyName != "fixture-night-light" ||
		device.Registration.BindingKey != "z2m-00124b0024abcd05" || device.Model != "3RSNL02043Z" {
		t.Fatalf("Device identity = %#v", device)
	}
	if device.Registration.Device.Kind != "light" {
		t.Fatalf("Device kind = %q, want light", device.Registration.Device.Kind)
	}
	want := []adapter.EntityDescriptor{
		{
			Key: "power", ExternalID: nightLightIEEE + "/root/power", Name: "Power",
			Type: "hearth.power/v1", Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
		},
		{
			Key: "brightness", ExternalID: nightLightIEEE + "/root/brightness", Name: "Brightness",
			Type:    "hearth.brightness/v1",
			Support: json.RawMessage(`{"state":{"maximum":100},"operations":{"set":{"step":1}}}`),
		},
		{
			Key: "colorxy", ExternalID: nightLightIEEE + "/root/colorxy", Name: "Color XY",
			Type:    "hearth.colorxy/v1",
			Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
		},
		{
			Key: "colormode", ExternalID: nightLightIEEE + "/root/colormode", Name: "Color Mode",
			Type: "hearth.colormode/v1", Support: json.RawMessage(`{"state":{},"operations":{}}`),
		},
		{
			Key: "poweronbehavior", ExternalID: nightLightIEEE + "/root/poweronbehavior", Name: "Power-On Behavior",
			Type:    "hearth.enumsetting/v1",
			Support: json.RawMessage(`{"state":{"choices":["off","on","toggle","previous"]},"operations":{"set":{}}}`),
		},
		{
			Key: "effect", ExternalID: nightLightIEEE + "/root/effect", Name: "Effect",
			Type: "hearth.enumaction/v1",
			Support: json.RawMessage(
				`{"state":{},"operations":{"trigger":{"values":["blink","breathe","okay","channel_change","finish_effect","stop_effect","colorloop","stop_colorloop"]}}}`,
			),
		},
		{
			Key: "illuminance", ExternalID: nightLightIEEE + "/root/illuminance", Name: "Illuminance",
			Type: "hearth.measurement/v1",
			Support: json.RawMessage(
				`{"state":{"maximum":1000000000,"measurement_kind":"illuminance","minimum":0,"unit":"lx"},"operations":{}}`,
			),
		},
		{
			Key: "occupancy", ExternalID: nightLightIEEE + "/root/occupancy", Name: "Occupancy",
			Type:    "hearth.binarysensor/v1",
			Support: json.RawMessage(`{"state":{},"operations":{}}`),
		},
		{
			Key: "linkquality", ExternalID: nightLightIEEE + "/root/linkquality", Name: "Link Quality",
			Type:    "hearth.numericsensor/v1",
			Support: json.RawMessage(`{"state":{"maximum":255,"minimum":0,"unit":"lqi"},"operations":{}}`),
		},
	}
	if !reflect.DeepEqual(device.Registration.Entities, want) {
		t.Fatalf("Entity descriptors = %#v, want %#v", device.Registration.Entities, want)
	}
	if err := validateEntityPlans(device.Entities); err != nil {
		t.Fatalf("night light plans rejected: %v", err)
	}
}

// This test protects the illuminance seam: the captured access-5 expose must
// register a stateful, read-only numeric lux sensor that requests a startup
// refresh for its own property and never a command route. The publish-only
// occupancy sibling must request no refresh despite sharing the message
// topic. It fails if illuminance gains a translator, loses its refresh, or
// if occupancy gains one.
func TestNightLightIlluminanceIsGettableReadOnlyLuxSensor(t *testing.T) {
	t.Parallel()
	device := mustNightLightDevice(t)
	illuminance := nightLightPlanByKey(t, device, "illuminance")
	if illuminance.StatePolicy != entityStateful ||
		!reflect.DeepEqual(illuminance.StateProperties, []string{"illuminance"}) ||
		!reflect.DeepEqual(illuminance.GetProperties, []string{"illuminance"}) ||
		illuminance.TranslateCommand != nil {
		t.Fatalf("illuminance plan = %#v, want stateful read-only lux sensor with refresh", illuminance)
	}
	occupancy := nightLightPlanByKey(t, device, "occupancy")
	if occupancy.StatePolicy != entityStateful ||
		!reflect.DeepEqual(occupancy.StateProperties, []string{"occupancy"}) ||
		len(occupancy.GetProperties) != 0 || occupancy.TranslateCommand != nil {
		t.Fatalf("occupancy plan = %#v, want stateful publish-only sensor", occupancy)
	}
}

// This test protects the captured payload contract independently of the
// planner: the live state message must decode the exact illuminance 20 and
// occupancy true readings with no issue. Expected literals come from the
// capture, never from the capability table or a production helper.
func TestNightLightCapturedStateDecodesIlluminanceAndOccupancy(t *testing.T) {
	t.Parallel()
	device := mustNightLightDevice(t)
	states, issues, err := decodeDeviceState(
		readFixture(t, "state-3rsnl02043z.json"),
		bindPlans(device.Entities),
		time.Unix(1, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("captured payload issues = %#v, want none", issues)
	}
	byEntity := make(map[string]decodedState, len(states))
	for _, state := range states {
		byEntity[state.entityID] = state
	}
	illuminance, ok := byEntity["entity-illuminance"]
	if !ok || string(illuminance.report.Observation.Value) != "20" ||
		illuminance.report.semantic != float64(20) {
		t.Fatalf("captured illuminance = %#v", illuminance)
	}
	occupancy, ok := byEntity["entity-occupancy"]
	if !ok || string(occupancy.report.Observation.Value) != "true" {
		t.Fatalf("captured occupancy = %#v", occupancy)
	}
}

// This test protects per-property rejection and sibling isolation with the
// captured device shape: an illuminance value outside the 0..1e9 envelope or
// of the wrong JSON shape must become exactly one illuminance issue while the
// valid occupancy reading from the same message still publishes. It fails if
// the adapter clamps, coerces, or defaults an invalid reading, or if one
// invalid property suppresses a valid sibling.
func TestNightLightInvalidIlluminanceKeepsOccupancy(t *testing.T) {
	t.Parallel()
	entities := bindPlans(mustNightLightDevice(t).Entities)
	for _, test := range []struct {
		name    string
		payload string
	}{
		{name: "negative", payload: `{"illuminance":-1,"occupancy":true}`},
		{name: "above envelope", payload: `{"illuminance":1000000001,"occupancy":true}`},
		{name: "numeric string", payload: `{"illuminance":"20","occupancy":true}`},
		{name: "null", payload: `{"illuminance":null,"occupancy":true}`},
		{name: "object", payload: `{"illuminance":{"value":20},"occupancy":true}`},
		{name: "overflow", payload: `{"illuminance":1e10000,"occupancy":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			states, issues, err := decodeDeviceState([]byte(test.payload), entities, time.Unix(1, 0).UTC())
			if err != nil {
				t.Fatal(err)
			}
			if len(issues) != 1 || !reflect.DeepEqual(issues[0].Properties, []string{"illuminance"}) {
				t.Fatalf("issues = %#v, want exactly one illuminance issue", issues)
			}
			for _, state := range states {
				if state.entityID == "entity-illuminance" {
					t.Fatalf("invalid illuminance published a State: %#v", state)
				}
			}
			if len(states) != 1 || states[0].entityID != "entity-occupancy" ||
				string(states[0].report.Observation.Value) != "true" {
				t.Fatalf("states = %#v, want the valid occupancy reading only", states)
			}
		})
	}
}

// This test protects read-only refresh separation for the illuminance
// capability across access shapes: get access alone controls the startup
// refresh, and neither shape gains a command route. It fails if publish-only
// illuminance requests a get the device never caches, or if gettable
// illuminance becomes controllable.
func TestNightLightIlluminanceSeparatesGetFromCommand(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		access  int
		wantGet bool
	}{
		{name: "publish only", access: 1, wantGet: false},
		{name: "publish and get", access: 5, wantGet: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device := mustNightLightDeviceWithIlluminanceAccess(t, test.access)
			plan := nightLightPlanByKey(t, device, "illuminance")
			if (len(plan.GetProperties) == 1) != test.wantGet ||
				plan.TranslateCommand != nil || plan.StatePolicy != entityStateful {
				t.Fatalf("illuminance access %d plan = %#v, want get=%t read-only", test.access, plan, test.wantGet)
			}
		})
	}
}

// This test protects the exact illuminance mapping gates on the captured
// Device: an incompatible unit or set access must omit only the illuminance
// Entity while the valid occupancy and linkquality siblings survive. It fails
// if the catalog widens the exact lx unit or admits a writable illuminance.
func TestNightLightIlluminanceRejectsWrongUnitAndSetAccess(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*upstreamExpose)
	}{
		{name: "wrong unit", edit: func(expose *upstreamExpose) { expose.Unit = "lm" }},
		{name: "empty unit", edit: func(expose *upstreamExpose) { expose.Unit = "" }},
		{name: "set access", edit: func(expose *upstreamExpose) { expose.Access = 7 }},
		{name: "get only access", edit: func(expose *upstreamExpose) { expose.Access = 4 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device := nightLightUpstreamDevice(t)
			index := nightLightIlluminanceIndex(t, device)
			test.edit(&device.Definition.Exposes[index])
			discovered, rejection := discoverDevice(device)
			if rejection != nil {
				t.Fatalf("Device rejected: %#v", rejection)
			}
			keys := entityKeys(discovered.Entities)
			if slices.Contains(keys, "illuminance") {
				t.Fatalf("ineligible illuminance registered: %v", keys)
			}
			if !slices.Contains(keys, "occupancy") || !slices.Contains(keys, "linkquality") {
				t.Fatalf("valid siblings suppressed: %v", keys)
			}
		})
	}
}

// nightLightUpstreamDevice decodes the sanitized fixture into a mutable
// upstream Device for eligibility edits.
func nightLightUpstreamDevice(t *testing.T) upstreamDevice {
	t.Helper()
	devices := fixtureDevices(t, nightLightFixtureName)
	if len(devices) != 1 {
		t.Fatalf("night light fixture must contain exactly one device, got %d", len(devices))
	}
	return devices[0]
}

// nightLightIlluminanceIndex finds the captured root illuminance expose.
func nightLightIlluminanceIndex(t *testing.T, device upstreamDevice) int {
	t.Helper()
	for index, expose := range device.Definition.Exposes {
		if expose.Type == upstreamExposeNumeric && expose.Name == "illuminance" {
			return index
		}
	}
	t.Fatal("night light fixture has no illuminance expose")
	return -1
}

// mustNightLightDeviceWithIlluminanceAccess discovers the fixture with the
// illuminance expose access bits replaced, so get/command separation can be
// asserted without editing the checked-in fixture.
func mustNightLightDeviceWithIlluminanceAccess(t *testing.T, access int) discoveredDevice {
	t.Helper()
	device := nightLightUpstreamDevice(t)
	device.Definition.Exposes[nightLightIlluminanceIndex(t, device)].Access = access
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	return discovered
}
