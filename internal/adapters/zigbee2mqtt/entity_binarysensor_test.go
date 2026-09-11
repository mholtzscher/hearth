package zigbee2mqtt //nolint:testpackage // Tests exercise package-private occupancy translation.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// occupancyExpose builds one device-root occupancy binary expose. The
// observed 3RSNL02043Z declaration is a publish-only boolean pair; tests edit
// the returned expose to prove declared-representation decoding and
// eligibility gating.
func occupancyExpose() upstreamExpose {
	return upstreamExpose{
		Type: "binary", Name: "occupancy", Property: "occupancy", Access: 1,
		ValueOn: json.RawMessage(`true`), ValueOff: json.RawMessage(`false`),
	}
}

// occupancyLightDevice is a light root with one normal occupancy supplement.
func occupancyLightDevice() upstreamDevice {
	device := eligibleDevice()
	device.Definition.Exposes = append(device.Definition.Exposes, occupancyExpose())
	return device
}

// occupancyOnlyDevice carries nothing but the occupancy root so an ineligible
// declaration has no primary family to fall back on.
func occupancyOnlyDevice() upstreamDevice {
	return upstreamDevice{
		IEEEAddress: "0x00124b0024abcdef", Type: "EndPoint", Supported: true,
		FriendlyName: "test-occupancy", InterviewState: "SUCCESSFUL",
		Endpoints: map[string]upstreamEndpoint{},
		Definition: &upstreamDefinition{
			Model: "TEST", Vendor: "Fixture", Description: "Fixture",
			Exposes: []upstreamExpose{occupancyExpose()},
		},
	}
}

func mustOccupancyEntities(t *testing.T, device upstreamDevice) []runtimeEntity {
	t.Helper()
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	return bindPlans(discovered.Entities)
}

func decodeOccupancy(
	t *testing.T,
	entities []runtimeEntity,
	payload string,
) ([]decodedState, []stateDecodeIssue) {
	t.Helper()
	states, issues, err := decodeDeviceState([]byte(payload), entities, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatalf("decode %s: %v", payload, err)
	}
	return states, issues
}

// This test protects externally visible registration: a light with a valid
// root occupancy expose keeps its light kind and gains one supplemental,
// read-only occupancy Entity of the generic binary sensor type.
func TestDiscoverOccupancyLightKeepsLightKind(t *testing.T) {
	t.Parallel()
	discovered, rejection := discoverDevice(occupancyLightDevice())
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if discovered.Registration.BindingKey != "z2m-00124b0024abcdef" ||
		discovered.Registration.Device.Kind != "light" {
		t.Fatalf("Device identity = %#v", discovered.Registration)
	}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"power", "brightness", "occupancy"}) {
		t.Fatalf("Entity keys = %v", got)
	}
	occupancy := discovered.Entities[2]
	wantSupport := json.RawMessage(`{"state":{},"operations":{}}`)
	if occupancy.Descriptor.Key != "occupancy" ||
		occupancy.Descriptor.ExternalID != "0x00124b0024abcdef/root/occupancy" ||
		occupancy.Descriptor.Name != "Occupancy" ||
		occupancy.Descriptor.Type != "hearth.binarysensor/v1" ||
		!reflect.DeepEqual(occupancy.Descriptor.Support, wantSupport) {
		t.Fatalf("occupancy descriptor = %#v", occupancy.Descriptor)
	}
	if !reflect.DeepEqual(occupancy.StateProperties, []string{"occupancy"}) ||
		len(occupancy.GetProperties) != 0 || occupancy.TranslateCommand != nil {
		t.Fatalf("publish-only occupancy plan = %#v", occupancy)
	}
}

// This test protects supplemental kind establishment: occupancy alone
// registers a sensor Device with no command route.
func TestDiscoverOccupancyOnlyDeviceRegistersSensor(t *testing.T) {
	t.Parallel()
	discovered, rejection := discoverDevice(occupancyOnlyDevice())
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if discovered.Registration.Device.Kind != "sensor" {
		t.Fatalf("Device kind = %q, want sensor", discovered.Registration.Device.Kind)
	}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"occupancy"}) {
		t.Fatalf("Entity keys = %v", got)
	}
}

// This test protects the captured live shape and fails if the real device's
// true/false occupancy payload does not decode to the declared on/off
// mapping. Expected values come from the expose declaration and the captured
// payload, never from a production helper.
func TestDecodeOccupancyLiveTrueFalseShape(t *testing.T) {
	t.Parallel()
	entities := mustOccupancyEntities(t, occupancyLightDevice())
	for _, test := range []struct {
		payload string
		want    string
	}{
		{payload: `{"occupancy":true}`, want: "true"},
		{payload: `{"occupancy":false}`, want: "false"},
		{payload: `{"state":"ON","occupancy":true}`, want: "true"},
	} {
		states, issues := decodeOccupancy(t, entities, test.payload)
		if len(issues) != 0 {
			t.Fatalf("decode %s issues = %#v", test.payload, issues)
		}
		got := ""
		for _, state := range states {
			if state.entityID == "entity-occupancy" {
				got = string(state.report.Observation.Value)
			}
		}
		if got != test.want {
			t.Errorf("decode %s occupancy = %q, want %q", test.payload, got, test.want)
		}
	}
}

// This test protects declared-representation decoding: an expose declaring
// string on/off values decodes those strings and rejects booleans, proving the
// adapter never assumes an upstream boolean.
func TestDecodeOccupancyHonorsDeclaredStringValues(t *testing.T) {
	t.Parallel()
	device := occupancyOnlyDevice()
	device.Definition.Exposes[0].ValueOn = json.RawMessage(`"ON"`)
	device.Definition.Exposes[0].ValueOff = json.RawMessage(`"OFF"`)
	entities := mustOccupancyEntities(t, device)
	states, issues := decodeOccupancy(t, entities, `{"occupancy":"ON"}`)
	if len(issues) != 0 || len(states) != 1 ||
		string(states[0].report.Observation.Value) != "true" {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
	states, issues = decodeOccupancy(t, entities, `{"occupancy":"OFF"}`)
	if len(issues) != 0 || len(states) != 1 ||
		string(states[0].report.Observation.Value) != "false" {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
	if _, issues = decodeOccupancy(t, entities, `{"occupancy":true}`); len(issues) != 1 {
		t.Fatalf("boolean against string declaration issues = %#v", issues)
	}
}

// This test protects per-value rejection and sibling isolation: values the
// declaration does not map become one occupancy issue while valid sibling
// State still publishes.
func TestDecodeOccupancyRejectsUnmappedValues(t *testing.T) {
	t.Parallel()
	entities := mustOccupancyEntities(t, occupancyLightDevice())
	for _, payload := range []string{
		`{"occupancy":"ON"}`,
		`{"occupancy":1}`,
		`{"occupancy":null}`,
		`{"occupancy":{"state":true}}`,
	} {
		states, issues := decodeOccupancy(t, entities, payload)
		if len(issues) != 1 || !reflect.DeepEqual(issues[0].Properties, []string{"occupancy"}) {
			t.Errorf("decode %s issues = %#v, want one occupancy issue", payload, issues)
		}
		for _, state := range states {
			if state.entityID == "entity-occupancy" {
				t.Errorf("decode %s published an occupancy State", payload)
			}
		}
	}
	states, issues := decodeOccupancy(t, entities, `{"state":"ON","occupancy":"ON"}`)
	if len(issues) != 1 || len(states) != 1 || states[0].entityID != "entity-power" {
		t.Fatalf("mixed payload states = %#v, issues = %#v", states, issues)
	}
}

// This test protects the occupancy eligibility gate and fails if an ambiguous
// root, a foreign property claim, missing or non-publish access, or an
// absent/indistinct/non-scalar declaration still registers an Entity.
func TestOccupancyPlanEligibility(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*upstreamDevice)
	}{
		{name: "duplicate occupancy roots", edit: func(device *upstreamDevice) {
			duplicate := occupancyExpose()
			duplicate.Property = "occupancy_2"
			device.Definition.Exposes = append(device.Definition.Exposes, duplicate)
		}},
		{name: "foreign property claim", edit: func(device *upstreamDevice) {
			device.Definition.Exposes = append(device.Definition.Exposes, upstreamExpose{
				Type: "binary", Name: "motion", Property: "occupancy", Access: 1,
				ValueOn: json.RawMessage(`true`), ValueOff: json.RawMessage(`false`),
			})
		}},
		{name: "empty property", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Property = ""
		}},
		{name: "renamed property", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Property = "presence"
		}},
		{name: "get-only access", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Access = 4
		}},
		{name: "set access", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Access = 1 | 2
		}},
		{name: "missing value_on", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].ValueOn = nil
		}},
		{name: "missing value_off", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].ValueOff = nil
		}},
		{name: "identical values", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].ValueOff = json.RawMessage(`true`)
		}},
		{name: "non-scalar value_on", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].ValueOn = json.RawMessage(`{"on":true}`)
		}},
		{name: "unresolved endpoint", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Endpoint = "missing"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device := occupancyOnlyDevice()
			test.edit(&device)
			_, rejection := discoverDevice(device)
			if rejection == nil || rejection.Code != rejectionNoEligibleEntity {
				t.Fatalf("rejection = %#v", rejection)
			}
		})
	}
}

// This test protects get-access handling: a gettable occupancy expose gains
// startup refresh without ever gaining a command route.
func TestOccupancyPlanSeparatesGetFromCommand(t *testing.T) {
	t.Parallel()
	device := occupancyOnlyDevice()
	device.Definition.Exposes[0].Access = 1 | 4
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	occupancy := discovered.Entities[0]
	if !reflect.DeepEqual(occupancy.GetProperties, []string{"occupancy"}) ||
		occupancy.TranslateCommand != nil {
		t.Fatalf("gettable occupancy plan = %#v", occupancy)
	}
}
