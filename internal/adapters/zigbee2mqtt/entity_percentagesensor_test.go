package zigbee2mqtt //nolint:testpackage // Tests exercise package-private percentage sensor translation.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

func eligiblePercentageSensorDevice(name string, access int) upstreamDevice {
	return upstreamDevice{
		IEEEAddress: "0x00124b0024abcdef", Type: "EndDevice", Supported: true,
		FriendlyName: "test-sensor", InterviewState: "SUCCESSFUL",
		Endpoints: map[string]upstreamEndpoint{},
		Definition: &upstreamDefinition{
			Model: "TEST", Vendor: "Fixture", Description: "Fixture",
			Exposes: []upstreamExpose{{
				Type: "numeric", Name: name, Property: name, Access: access, Unit: "%",
			}},
		},
	}
}

func eligibleHumidityBatteryDevice(access int) upstreamDevice {
	device := eligiblePercentageSensorDevice("humidity", access)
	device.Definition.Exposes = append(
		device.Definition.Exposes,
		eligiblePercentageSensorDevice("battery", access).Definition.Exposes...,
	)
	return device
}

// This test protects percentage bounds and fractional readings and fails on
// truncation, clamping, numeric-string coercion, or huge-exponent acceptance.
func TestNormalizePercentageSensorTable(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		payload string
		want    float64
		valid   bool
	}{
		{payload: `0`, want: 0, valid: true},
		{payload: `100`, want: 100, valid: true},
		{payload: `48.2`, want: 48.2, valid: true},
		{payload: `2.5e1`, want: 25, valid: true},
		{payload: `0.1`, want: 0.1, valid: true},
		{payload: `99.99`, want: 99.99, valid: true},
		{payload: `100.0`, want: 100, valid: true},
		{payload: `"48.2"`},
		{payload: `null`},
		{payload: `true`},
		{payload: `1e10000`},
		{payload: `-0.1`},
		{payload: `100.1`},
		{payload: `100.00000000000000001`},
		{payload: `-1e-1000`},
		{payload: `48.2 trailing`},
		{payload: `{}`},
	} {
		got, err := normalizePercentageSensor(json.RawMessage(test.payload))
		if (err == nil) != test.valid || got != test.want {
			t.Errorf(
				"normalizePercentageSensor(%s) = %v, %v; want %v, valid=%t",
				test.payload,
				got,
				err,
				test.want,
				test.valid,
			)
		}
	}
}

// This test protects humidity and battery registration and fails if either
// Entity misses its percent support, read-only plan, or stable identity.
func TestDiscoverHumidityBatteryRegistersPercentSupport(t *testing.T) {
	t.Parallel()
	discovered, rejection := discoverDevice(eligibleHumidityBatteryDevice(1))
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if discovered.Registration.BindingKey != "z2m-00124b0024abcdef" ||
		discovered.Registration.Device.Kind != "sensor" {
		t.Fatalf("Device identity = %#v", discovered.Registration)
	}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"humidity", "battery"}) {
		t.Fatalf("Entity keys = %v", got)
	}
	wantSupport := json.RawMessage(`{"state":{"maximum":100,"minimum":0,"unit":"%"},"operations":{}}`)
	for index, want := range []struct {
		key        string
		externalID string
		name       string
	}{
		{key: "humidity", externalID: "0x00124b0024abcdef/root/humidity", name: "Humidity"},
		{key: "battery", externalID: "0x00124b0024abcdef/root/battery", name: "Battery"},
	} {
		entity := discovered.Entities[index]
		if entity.Descriptor.Key != want.key ||
			entity.Descriptor.ExternalID != want.externalID ||
			entity.Descriptor.Name != want.name || entity.Descriptor.Type != "hearth.numericsensor/v1" ||
			!reflect.DeepEqual(entity.Descriptor.Support, wantSupport) {
			t.Fatalf("percent descriptor %d = %#v", index, entity.Descriptor)
		}
		if !reflect.DeepEqual(entity.StateProperties, []string{want.key}) ||
			len(entity.GetProperties) != 0 || entity.TranslateCommand != nil {
			t.Fatalf("publish-only percent plan %d = %#v", index, entity)
		}
	}
}

// This test protects the temperature-first merge order and fails if humidity
// or battery disturb the existing temperature identity.
func TestDiscoverTemperatureHumidityBatteryKeepsOrder(t *testing.T) {
	t.Parallel()
	device := eligibleSensorDevice("temperature", 1)
	device.Definition.Exposes = append(
		device.Definition.Exposes,
		eligibleHumidityBatteryDevice(1).Definition.Exposes...,
	)
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"temperature", "humidity", "battery"}) {
		t.Fatalf("Entity keys = %v", got)
	}
}

// This test protects read-only access separation and fails if a publish-only
// percent sensor gains startup refresh or a gettable sensor gains a command
// route.
func TestPercentageSensorPlanSeparatesGetFromCommand(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"humidity", "battery"} {
		publishOnly, rejection := discoverDevice(eligiblePercentageSensorDevice(name, 1))
		if rejection != nil {
			t.Fatalf("%s Device rejected: %#v", name, rejection)
		}
		if len(publishOnly.Entities) != 1 ||
			len(publishOnly.Entities[0].GetProperties) != 0 ||
			publishOnly.Entities[0].TranslateCommand != nil {
			t.Fatalf("publish-only %s plan = %#v", name, publishOnly.Entities)
		}
		gettable, rejection := discoverDevice(eligiblePercentageSensorDevice(name, 1|4))
		if rejection != nil {
			t.Fatalf("%s Device rejected: %#v", name, rejection)
		}
		if !reflect.DeepEqual(gettable.Entities[0].GetProperties, []string{name}) ||
			gettable.Entities[0].TranslateCommand != nil {
			t.Fatalf("gettable %s plan = %#v", name, gettable.Entities[0])
		}
		_, rejection = discoverDevice(eligiblePercentageSensorDevice(name, 1|2))
		if rejection == nil || rejection.Code != rejectionNoEligibleEntity {
			t.Fatalf("settable %s rejection = %#v", name, rejection)
		}
	}
}

// This test protects the percent eligibility gate and fails if unit,
// property, access, endpoint, or name violations still register.
func TestPercentageSensorPlanEligibility(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*upstreamDevice)
	}{
		{name: "wrong unit", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Unit = "lqi"
		}},
		{name: "missing unit", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Unit = ""
		}},
		{name: "empty property", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Property = ""
		}},
		{name: "missing publish access", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Access = 4
		}},
		{name: "unresolved endpoint", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Endpoint = "missing"
		}},
		{name: "duplicated property", edit: func(device *upstreamDevice) {
			duplicate := device.Definition.Exposes[0]
			duplicate.Name = "humidity"
			device.Definition.Exposes = append(device.Definition.Exposes, duplicate)
		}},
		{name: "arbitrary numeric expose", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Name = "soil_moisture"
		}},
		{name: "settable comfort bound", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Name = "comfort_humidity_min"
			device.Definition.Exposes[0].Access = 7
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device := eligiblePercentageSensorDevice("battery", 1)
			test.edit(&device)
			_, rejection := discoverDevice(device)
			if rejection == nil || rejection.Code != rejectionNoEligibleEntity {
				t.Fatalf("rejection = %#v", rejection)
			}
		})
	}
}

// This test protects endpoint-scoped percent identity and fails if the
// sensor planner ignores endpoint labels or custom property names.
func TestDiscoverPercentageSensorEndpointIdentity(t *testing.T) {
	t.Parallel()
	device := eligiblePercentageSensorDevice("humidity_right", 1|4)
	device.Definition.Exposes[0].Name = "humidity"
	device.Endpoints = map[string]upstreamEndpoint{"2": {Name: "right"}}
	device.Definition.Exposes[0].Endpoint = "right"
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if len(discovered.Entities) != 1 {
		t.Fatalf("Entities = %#v", discovered.Entities)
	}
	humidity := discovered.Entities[0]
	if humidity.Descriptor.Key != "humidity-ep2" || humidity.Descriptor.Name != "right Humidity" ||
		humidity.Descriptor.ExternalID != "0x00124b0024abcdef/ep2/humidity" {
		t.Fatalf("endpoint humidity descriptor = %#v", humidity.Descriptor)
	}
	if !reflect.DeepEqual(humidity.StateProperties, []string{"humidity_right"}) ||
		!reflect.DeepEqual(humidity.GetProperties, []string{"humidity_right"}) {
		t.Fatalf("endpoint humidity plan = %#v", humidity)
	}
}

// This test protects per-property sibling isolation and fails if a missing,
// non-numeric, or out-of-range percent reading suppresses valid siblings or
// produces synthetic State.
func TestDecodePercentageSensorStateIsolation(t *testing.T) {
	t.Parallel()
	device := eligibleSensorDevice("temperature", 1)
	device.Definition.Exposes = append(
		device.Definition.Exposes,
		eligibleHumidityBatteryDevice(1).Definition.Exposes...,
	)
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	entities := bindPlans(discovered.Entities)
	receivedAt := time.Unix(1, 0).UTC()

	complete, issues, err := decodeDeviceState(
		[]byte(`{"temperature":22.6,"humidity":48.2,"battery":100}`),
		entities,
		receivedAt,
	)
	if err != nil || len(issues) != 0 || len(complete) != 3 ||
		string(complete[1].report.Observation.Value) != "48.2" ||
		string(complete[2].report.Observation.Value) != "100" {
		t.Fatalf("complete states = %#v, issues = %#v, err = %v", complete, issues, err)
	}
	if semantic, ok := complete[1].report.semantic.(float64); !ok || semantic != 48.2 {
		t.Fatalf("humidity semantic = %#v", complete[1].report.semantic)
	}

	partial, issues, err := decodeDeviceState(
		[]byte(`{"temperature":22.6,"humidity":48.2}`),
		entities,
		receivedAt,
	)
	if err != nil || len(issues) != 0 || len(partial) != 2 {
		t.Fatalf("partial states = %#v, issues = %#v, err = %v", partial, issues, err)
	}

	for _, test := range []struct {
		payload     string
		wantStates  int
		wantIssues  int
		wantProblem []string
	}{
		{
			payload:     `{"temperature":22.6,"humidity":"damp","battery":100}`,
			wantStates:  2,
			wantIssues:  1,
			wantProblem: []string{"humidity"},
		},
		{
			payload:     `{"temperature":22.6,"humidity":48.2,"battery":100.1}`,
			wantStates:  2,
			wantIssues:  1,
			wantProblem: []string{"battery"},
		},
		{
			payload:     `{"temperature":22.6,"humidity":-0.1,"battery":null}`,
			wantStates:  1,
			wantIssues:  2,
			wantProblem: []string{"humidity", "battery"},
		},
	} {
		states, stateIssues, decodeErr := decodeDeviceState([]byte(test.payload), entities, receivedAt)
		if decodeErr != nil || len(states) != test.wantStates || len(stateIssues) != test.wantIssues {
			t.Fatalf(
				"invalid %s: states=%#v issues=%#v err=%v",
				test.payload,
				states,
				stateIssues,
				decodeErr,
			)
		}
		if string(states[0].report.Observation.Value) != "22600" {
			t.Fatalf("invalid %s suppressed temperature: %#v", test.payload, states)
		}
		for index, property := range test.wantProblem {
			if !reflect.DeepEqual(stateIssues[index].Properties, []string{property}) {
				t.Fatalf("invalid %s issue %d = %#v", test.payload, index, stateIssues[index])
			}
		}
	}
}

// This test protects read-only reconciliation and fails if a humidity Entity
// creates a command route or a publish-only percent sensor triggers startup
// refresh.
func TestReconcileSeparatesHumidityRoutesFromRefresh(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		access      int
		wantRoutes  int
		wantRefresh int
	}{
		{name: "publish-only", access: 1, wantRoutes: 0, wantRefresh: 0},
		{name: "gettable", access: 1 | 4, wantRoutes: 0, wantRefresh: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			published := reconcileSensorDevice(t, eligiblePercentageSensorDevice("humidity", test.access))
			if len(published.routes) != test.wantRoutes || len(published.devices) != 1 {
				t.Fatalf("routes=%d devices=%d", len(published.routes), len(published.devices))
			}
			if len(published.refresh) != test.wantRefresh {
				t.Fatalf("refresh publications = %#v", published.refresh)
			}
			if test.wantRefresh == 1 && (published.refresh[0].payload != `{"humidity":""}` ||
				published.refresh[0].qos != mqttQoS || published.refresh[0].retained) {
				t.Fatalf("refresh publication = %#v", published.refresh[0])
			}
		})
	}
}

// This test protects pre-registration descriptor completeness and fails
// if a percent plan built with empty metadata can pass validation: the
// shared constructor defers completeness to validateEntityPlans.
func TestPercentageSensorDecodeAndValidationSeams(t *testing.T) {
	t.Parallel()
	plan, err := newPercentageSensorPlan(adapter.EntityMetadata{
		Key:        "humidity",
		ExternalID: "0x1/root/humidity",
		Name:       "Humidity",
	}, "humidity", false)
	if err != nil {
		t.Fatal(err)
	}
	_, _, decodeErr := plan.DecodeState(
		"entity-humidity",
		map[string]json.RawMessage{"humidity": json.RawMessage(`"damp"`)},
		time.Unix(1, 0).UTC(),
	)
	if decodeErr == nil {
		t.Fatal("non-numeric humidity was accepted")
	}
	incomplete, err := newPercentageSensorPlan(adapter.EntityMetadata{}, "battery", false)
	if err != nil {
		t.Fatal(err)
	}
	if validateErr := validateEntityPlans([]entityPlan{incomplete}); validateErr == nil {
		t.Fatal("percent plan with empty metadata passed validation")
	}
}
