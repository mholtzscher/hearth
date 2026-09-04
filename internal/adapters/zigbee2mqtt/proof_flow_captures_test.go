package zigbee2mqtt //nolint:testpackage // Tests exercise package-private discovery and translation routes.

// Fixture provenance: Zigbee2MQTT 2.13.0 with zigbee-herdsman-converters
// 26.90.0. Inventory captures are sanitized real shapes for the SONOFF S31ZB
// smart plug and the SONOFF SNZB-02D temperature/humidity sensor. Sanitization
// changed IEEE addresses, friendly names, descriptions, and network identifiers
// only. Vendor, model, expose nesting, type, name, property, endpoint, access,
// unit, scalar values, and numeric bounds are retained.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// This test protects the captured relay proof Device and fails on hard-coded
// ON/OFF scalars, a non-relay Device kind, power identity that diverges from
// lights, or linkquality leakage into Entities.
func TestDiscoverCapturedRelayPlug(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-relay-plug.json")
	if device.IEEEAddress != "0x00124b0024abcd01" || device.FriendlyName != "fixture-plug" ||
		device.Registration.BindingKey != "z2m-00124b0024abcd01" {
		t.Fatalf("Device identity = %#v", device)
	}
	if device.Registration.Device.ExternalID == nil ||
		*device.Registration.Device.ExternalID != "0x00124b0024abcd01" ||
		device.Registration.Device.Name != "Sanitized Fixture Plug" ||
		device.Registration.Device.Kind != "relay" {
		t.Fatalf("Device descriptor = %#v", device.Registration.Device)
	}
	if device.Model != "S31ZB" {
		t.Fatalf("Device model = %q, want S31ZB", device.Model)
	}
	want := []struct {
		key        string
		externalID string
		name       string
		entityType string
		support    string
	}{
		{
			key: "power", externalID: "0x00124b0024abcd01/root/power", name: "Power",
			entityType: "hearth.power/v1", support: `{"state":{},"operations":{"set":{}}}`,
		},
	}
	if len(device.Registration.Entities) != len(want) || len(device.Entities) != len(want) {
		t.Fatalf("Entities = %#v", device.Entities)
	}
	for index, entity := range want {
		got := device.Registration.Entities[index]
		if got.Key != entity.key || got.ExternalID != entity.externalID || got.Name != entity.name ||
			got.Type != entity.entityType || string(got.Support) != entity.support {
			t.Fatalf("Entity descriptor %d = %#v, want %#v", index, got, entity)
		}
		plan := device.Entities[index]
		if !reflect.DeepEqual(plan.StateProperties, []string{"state"}) ||
			!reflect.DeepEqual(plan.GetProperties, []string{"state"}) || plan.TranslateCommand == nil {
			t.Fatalf("power plan %d = %#v", index, plan)
		}
	}
}

// This test protects captured relay State projection and fails if the plug
// State ignores its discovered ON scalar, if linkquality produces State, or if
// sibling isolation is lost.
func TestDecodeCapturedRelayPlugState(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-relay-plug.json")
	states, issues, err := decodeDeviceState(
		readFixture(t, "state-relay-plug.json"),
		bindPlans(device.Entities),
		time.Unix(1, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(states) != 1 || states[0].entityID != "entity-power" ||
		string(states[0].report.Observation.Value) != "true" {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
}

// This test protects captured relay Command translation and fails if the plug
// publishes anything other than its discovered ON/OFF scalars or if the
// outcome matcher accepts the opposite power.
func TestCapturedRelayPlugCommandTranslation(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-relay-plug.json")
	entities := bindPlans(device.Entities)
	route := commandRoute{entityID: "entity-power", entity: entities[0]}
	for _, test := range []struct {
		parameters string
		payload    string
		value      bool
	}{
		{parameters: `{"value":true}`, payload: `{"state":"ON"}`, value: true},
		{parameters: `{"value":false}`, payload: `{"state":"OFF"}`, value: false},
	} {
		recorder := &runtimeRecorder{}
		payload, planned, err := translateCommand(
			context.Background(),
			route,
			testCommand("entity-power", test.parameters),
			newFakeResponder(recorder, newFakeSession(recorder)),
		)
		if err != nil {
			t.Fatal(err)
		}
		if string(payload) != test.payload {
			t.Fatalf("payload = %s, want %s", payload, test.payload)
		}
		if !planned.Matches(stateReport{semantic: test.value}) ||
			planned.Matches(stateReport{semantic: !test.value}) {
			t.Fatalf("matcher did not enforce commanded power %t", test.value)
		}
	}
}

// This test protects the captured temperature proof Device and fails if the
// sensor registers anything other than one read-only milli-Celsius Entity, if
// humidity/battery/linkquality siblings leak into Entities, or if support
// gains Operations.
func TestDiscoverCapturedTemperatureSensor(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-temperature.json")
	if device.IEEEAddress != "0x00124b0024abcd02" || device.FriendlyName != "fixture-temperature" ||
		device.Registration.BindingKey != "z2m-00124b0024abcd02" {
		t.Fatalf("Device identity = %#v", device)
	}
	if device.Registration.Device.ExternalID == nil ||
		*device.Registration.Device.ExternalID != "0x00124b0024abcd02" ||
		device.Registration.Device.Name != "Sanitized Fixture Temperature" ||
		device.Registration.Device.Kind != "sensor" {
		t.Fatalf("Device descriptor = %#v", device.Registration.Device)
	}
	if device.Model != "SNZB-02D" {
		t.Fatalf("Device model = %q, want SNZB-02D", device.Model)
	}
	if len(device.Registration.Entities) != 1 || len(device.Entities) != 1 {
		t.Fatalf("Entities = %#v", device.Entities)
	}
	descriptor := device.Registration.Entities[0]
	wantSupport := json.RawMessage(`{"state":{},"operations":{}}`)
	if descriptor.Key != "temperature" ||
		descriptor.ExternalID != "0x00124b0024abcd02/root/temperature" ||
		descriptor.Name != "Temperature" || descriptor.Type != "hearth.temperature/v1" ||
		!reflect.DeepEqual(descriptor.Support, wantSupport) {
		t.Fatalf("temperature descriptor = %#v", descriptor)
	}
	plan := device.Entities[0]
	if !reflect.DeepEqual(plan.StateProperties, []string{"temperature"}) ||
		!reflect.DeepEqual(plan.GetProperties, []string{"temperature"}) ||
		plan.TranslateCommand != nil {
		t.Fatalf("gettable temperature plan = %#v", plan)
	}
}

// This test protects captured temperature State projection and fails if 22.6
// °C does not publish exactly 22600 milli-Celsius, if humidity/battery
// siblings produce State, or if support is not empty.
func TestDecodeCapturedTemperatureState(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-temperature.json")
	states, issues, err := decodeDeviceState(
		readFixture(t, "state-temperature.json"),
		bindPlans(device.Entities),
		time.Unix(1, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(states) != 1 || states[0].entityID != "entity-temperature" ||
		string(states[0].report.Observation.Value) != "22600" {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
	if semantic, ok := states[0].report.semantic.(int64); !ok || semantic != 22600 {
		t.Fatalf("temperature semantic = %#v", states[0].report.semantic)
	}
}
