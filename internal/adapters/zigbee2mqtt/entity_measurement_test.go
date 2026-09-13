package zigbee2mqtt //nolint:testpackage // Tests exercise package-private measurement translation.

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkmeasurementv1 "github.com/mholtzscher/hearth/sdk/adapter/measurementv1"
)

// measurementSupport returns the production support for one measurement kind,
// failing when the catalog no longer maps that kind.
func measurementSupport(t *testing.T, kind string) sdkmeasurementv1.Support {
	t.Helper()
	for _, mapping := range measurementMappings() {
		if mapping.measurementKind == kind {
			return mapping.support()
		}
	}
	t.Fatalf("measurement kind %q missing from catalog", kind)
	return sdkmeasurementv1.Support{}
}

// mustMeasurementMapping returns the production mapping for one kind.
func mustMeasurementMapping(t *testing.T, kind string) measurementMapping {
	t.Helper()
	for _, mapping := range measurementMappings() {
		if mapping.measurementKind == kind {
			return mapping
		}
	}
	t.Fatalf("measurement kind %q missing from catalog", kind)
	return measurementMapping{}
}

// mustMeasurementPlan builds one publish-only plan through the shared
// constructor using the production support for the requested kind.
func mustMeasurementPlan(t *testing.T, kind string) entityPlan {
	t.Helper()
	mapping := mustMeasurementMapping(t, kind)
	plan, err := newMeasurementPlan(adapter.EntityMetadata{
		Key:        mapping.key,
		ExternalID: "0x00124b0024abcdef/root/" + mapping.key,
		Name:       mapping.displayName,
	}, mapping.key, mapping.support(), false)
	if err != nil {
		t.Fatalf("measurement plan %q was rejected: %v", kind, err)
	}
	return plan
}

// eligibleMeasurementDevice builds one synthetic sensor Device with a single
// literal numeric expose, independent of the production capability table.
func eligibleMeasurementDevice(exposeName, unit string, access int) upstreamDevice {
	return upstreamDevice{
		IEEEAddress: "0x00124b0024abcdef", Type: "EndDevice", Supported: true,
		FriendlyName: "test-sensor", InterviewState: "SUCCESSFUL",
		Endpoints: map[string]upstreamEndpoint{},
		Definition: &upstreamDefinition{
			Model: "TEST", Vendor: "Fixture", Description: "Fixture",
			Exposes: []upstreamExpose{{
				Type: "numeric", Name: exposeName, Property: exposeName, Access: access, Unit: unit,
			}},
		},
	}
}

// eligibleMeasurementSiblingsDevice builds the synthetic temperature fixture
// with temperature, humidity, and battery measurement exposes.
func eligibleMeasurementSiblingsDevice(access int) upstreamDevice {
	device := eligibleMeasurementDevice("temperature", "°C", access)
	device.Definition.Exposes = append(
		device.Definition.Exposes,
		eligibleMeasurementDevice("humidity", "%", access).Definition.Exposes...,
	)
	device.Definition.Exposes = append(
		device.Definition.Exposes,
		eligibleMeasurementDevice("battery", "%", access).Definition.Exposes...,
	)
	return device
}

// This test pins the four initial semantic measurement capabilities to their
// literal expose names, Entity keys, display names, exact upstream units,
// measurement kinds, canonical units, and kind-wide envelopes in catalog
// order. It fails if any migrated kind drifts, is dropped, or is reordered.
func TestMeasurementMappingsPinInitialKinds(t *testing.T) {
	t.Parallel()
	want := []measurementMapping{
		{
			exposeName: "temperature", key: "temperature", displayName: "Temperature",
			upstreamUnit: "°C", measurementKind: "temperature", canonicalUnit: "Cel",
			minimum: -273.15, maximum: 1000,
		},
		{
			exposeName: "humidity", key: "humidity", displayName: "Humidity",
			upstreamUnit: "%", measurementKind: "relative_humidity", canonicalUnit: "%",
			minimum: 0, maximum: 100,
		},
		{
			exposeName: "illuminance", key: "illuminance", displayName: "Illuminance",
			upstreamUnit: "lx", measurementKind: "illuminance", canonicalUnit: "lx",
			minimum: 0, maximum: 1e9,
		},
		{
			exposeName: "battery", key: "battery", displayName: "Battery",
			upstreamUnit: "%", measurementKind: "battery_level", canonicalUnit: "%",
			minimum: 0, maximum: 100,
		},
	}
	if got := measurementMappings(); !reflect.DeepEqual(got, want) {
		t.Fatalf("measurement mappings = %#v, want %#v", got, want)
	}
}

// This test protects literal descriptor/state semantics for every migrated
// kind. It fails if a kind registers a different type, kind, unit, or bounds,
// or if the decoded magnitude is rescaled (notably 21.5 °C must stay 21.5,
// never 21500 milli-Celsius).
func TestMeasurementPlanTranslatesCanonicalState(t *testing.T) {
	t.Parallel()
	receivedAt := time.Unix(1, 0).UTC()
	for _, test := range []struct {
		kind     string
		property string
		payload  string
		want     float64
		support  string
	}{
		{
			kind: "temperature", property: "temperature", payload: `21.5`, want: 21.5,
			support: `{"state":{"maximum":1000,"measurement_kind":"temperature",` +
				`"minimum":-273.15,"unit":"Cel"},"operations":{}}`,
		},
		{
			kind: "relative_humidity", property: "humidity", payload: `48.2`, want: 48.2,
			support: `{"state":{"maximum":100,"measurement_kind":"relative_humidity",` +
				`"minimum":0,"unit":"%"},"operations":{}}`,
		},
		{
			kind: "illuminance", property: "illuminance", payload: `20`, want: 20,
			support: `{"state":{"maximum":1000000000,"measurement_kind":"illuminance",` +
				`"minimum":0,"unit":"lx"},"operations":{}}`,
		},
		{
			kind: "battery_level", property: "battery", payload: `99.25`, want: 99.25,
			support: `{"state":{"maximum":100,"measurement_kind":"battery_level",` +
				`"minimum":0,"unit":"%"},"operations":{}}`,
		},
	} {
		plan := mustMeasurementPlan(t, test.kind)
		if plan.Descriptor.Type != "hearth.measurement/v1" ||
			string(plan.Descriptor.Support) != test.support {
			t.Fatalf("%s descriptor = %#v", test.kind, plan.Descriptor)
		}
		report := contractDecode(t, plan, test.property, test.payload)
		if semantic, ok := report.semantic.(float64); !ok || semantic != test.want {
			t.Fatalf("%s decode of %s = %#v, want %v",
				test.kind, test.payload, report.semantic, test.want)
		}
		value, _, err := plan.DecodeState("entity-test", map[string]json.RawMessage{
			test.property: json.RawMessage(test.payload),
		}, receivedAt)
		if err != nil || string(value.Observation.Value) != test.payload {
			t.Fatalf("%s Observation = %s, err = %v; want %s",
				test.kind, value.Observation.Value, err, test.payload)
		}
	}
}

// This test protects contract range and malformed-value rejection at the plan
// boundary for every migrated kind. It fails if an out-of-range, non-number,
// overflowing, or trailing value decodes, or if a boundary reading is lost.
func TestMeasurementPlanStateRangeBoundary(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		kind  string
		min   string
		max   string
		below string
		above string
	}{
		{
			kind: "temperature", min: `-273.15`, max: `1000`,
			below: `-273.151`, above: `1000.001`,
		},
		{
			kind: "relative_humidity", min: `0`, max: `100`,
			below: `-0.1`, above: `100.1`,
		},
		{
			kind: "illuminance", min: `0`, max: `1000000000`,
			below: `-0.1`, above: `1000000000.1`,
		},
		{
			kind: "battery_level", min: `0`, max: `100`,
			below: `-0.1`, above: `100.1`,
		},
	} {
		t.Run(test.kind, func(t *testing.T) {
			t.Parallel()
			assertMeasurementPlanBoundary(t, test.kind, test.min, test.max, test.below, test.above)
		})
	}
}

// assertMeasurementPlanBoundary checks one mapping's acceptance and rejection
// boundary, including malformed shapes and a missing property.
func assertMeasurementPlanBoundary(t *testing.T, kind, minimum, maximum, below, above string) {
	t.Helper()
	receivedAt := time.Unix(1, 0).UTC()
	mapping := mustMeasurementMapping(t, kind)
	wantMin, err := jsonNumberValue(minimum)
	if err != nil {
		t.Fatal(err)
	}
	wantMax, err := jsonNumberValue(maximum)
	if err != nil {
		t.Fatal(err)
	}
	plan := mustMeasurementPlan(t, kind)
	for _, testCase := range []struct {
		payload string
		want    float64
		valid   bool
	}{
		{payload: minimum, want: wantMin, valid: true},
		{payload: maximum, want: wantMax, valid: true},
		{payload: below},
		{payload: above},
		{payload: `"` + minimum + `"`},
		{payload: `null`},
		{payload: `true`},
		{payload: `1e10000`},
		{payload: `${}`},
		{payload: minimum + ` trailing`},
	} {
		report, decoded, decodeErr := plan.DecodeState(
			"entity-test",
			map[string]json.RawMessage{mapping.key: json.RawMessage(testCase.payload)},
			receivedAt,
		)
		if (decodeErr == nil) != testCase.valid || decoded != testCase.valid {
			t.Errorf("%s DecodeState(%s) decoded=%t, err=%v; want valid=%t",
				kind, testCase.payload, decoded, decodeErr, testCase.valid)
			continue
		}
		if !testCase.valid {
			continue
		}
		if semantic, ok := report.semantic.(float64); !ok || semantic != testCase.want {
			t.Errorf("%s DecodeState(%s) semantic = %#v; want %v",
				kind, testCase.payload, report.semantic, testCase.want)
		}
	}
	if _, decoded, decodeErr := plan.DecodeState(
		"entity-test", map[string]json.RawMessage{}, receivedAt,
	); decodeErr != nil || decoded {
		t.Errorf("%s DecodeState(missing) decoded=%t, err=%v; want absent", kind, decoded, decodeErr)
	}
}

// This test protects documented binary64 normalization at the adapter
// boundary: a high-precision literal above a whole value reads as that whole
// value, and an underflowing exponent reads as zero. It fails if the plan
// truncates, re-rounds, or rejects representable input.
func TestMeasurementPlanBinary64Normalization(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		kind    string
		payload string
		want    float64
	}{
		{kind: "relative_humidity", payload: `100.00000000000000001`, want: 100},
		{kind: "relative_humidity", payload: `-1e-1000`, want: 0},
		{kind: "battery_level", payload: `100.00000000000000001`, want: 100},
		{kind: "battery_level", payload: `-1e-1000`, want: 0},
	} {
		mapping := mustMeasurementMapping(t, test.kind)
		report := contractDecode(t, mustMeasurementPlan(t, test.kind), mapping.key, test.payload)
		if semantic, ok := report.semantic.(float64); !ok || semantic != test.want {
			t.Fatalf("%s decode of %s = %#v, want %v",
				test.kind, test.payload, report.semantic, test.want)
		}
	}
}

// jsonNumberValue parses one literal JSON number used as a test expectation.
func jsonNumberValue(literal string) (float64, error) {
	var value float64
	if err := json.Unmarshal([]byte(literal), &value); err != nil {
		return 0, err
	}
	return value, nil
}

// This property test protects finite fractional magnitudes across the whole
// temperature envelope and fails if any accepted binary64 value is rescaled,
// truncated, or rounded by the measurement plan.
func TestMeasurementPlanPreservesBinary64Fraction(t *testing.T) {
	t.Parallel()
	plan := mustMeasurementPlan(t, "temperature")
	receivedAt := time.Unix(1, 0).UTC()
	rapid.Check(t, func(t *rapid.T) {
		value := rapid.Float64Range(-273.15, 1000).Draw(t, "celsius")
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		report, decoded, decodeErr := plan.DecodeState("entity-temperature",
			map[string]json.RawMessage{"temperature": payload}, receivedAt)
		if decodeErr != nil || !decoded {
			t.Fatalf("DecodeState(%s) decoded=%t, err=%v", payload, decoded, decodeErr)
		}
		if semantic, ok := report.semantic.(float64); !ok || semantic != value {
			t.Fatalf("DecodeState(%s) semantic = %#v, want %v", payload, report.semantic, value)
		}
		var roundTripped float64
		if unmarshalErr := json.Unmarshal(report.Observation.Value, &roundTripped); unmarshalErr != nil ||
			roundTripped != value {
			t.Fatalf("Observation %s round-tripped to %v, err=%v; want %v",
				report.Observation.Value, roundTripped, unmarshalErr, value)
		}
	})
}

// This test protects discovery of the synthetic temperature fixture and fails
// if any migrated kind loses its literal type, support, identity, or
// publish-only route.
func TestDiscoverMeasurementKindsRegisterLiteralSupport(t *testing.T) {
	t.Parallel()
	discovered, rejection := discoverDevice(eligibleMeasurementSiblingsDevice(1))
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if discovered.Registration.BindingKey != "z2m-00124b0024abcdef" ||
		discovered.Registration.Device.Kind != "sensor" {
		t.Fatalf("Device identity = %#v", discovered.Registration)
	}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"temperature", "humidity", "battery"}) {
		t.Fatalf("Entity keys = %v", got)
	}
	want := []struct {
		key        string
		externalID string
		name       string
		support    string
	}{
		{
			key: "temperature", externalID: "0x00124b0024abcdef/root/temperature", name: "Temperature",
			support: `{"state":{"maximum":1000,"measurement_kind":"temperature",` +
				`"minimum":-273.15,"unit":"Cel"},"operations":{}}`,
		},
		{
			key: "humidity", externalID: "0x00124b0024abcdef/root/humidity", name: "Humidity",
			support: `{"state":{"maximum":100,"measurement_kind":"relative_humidity",` +
				`"minimum":0,"unit":"%"},"operations":{}}`,
		},
		{
			key: "battery", externalID: "0x00124b0024abcdef/root/battery", name: "Battery",
			support: `{"state":{"maximum":100,"measurement_kind":"battery_level",` +
				`"minimum":0,"unit":"%"},"operations":{}}`,
		},
	}
	for index, wantEntity := range want {
		entity := discovered.Entities[index]
		if entity.Descriptor.Key != wantEntity.key ||
			entity.Descriptor.ExternalID != wantEntity.externalID ||
			entity.Descriptor.Name != wantEntity.name || entity.Descriptor.Type != "hearth.measurement/v1" ||
			string(entity.Descriptor.Support) != wantEntity.support {
			t.Fatalf("measurement descriptor %d = %#v", index, entity.Descriptor)
		}
		if !reflect.DeepEqual(entity.StateProperties, []string{wantEntity.key}) ||
			len(entity.GetProperties) != 0 || entity.TranslateCommand != nil {
			t.Fatalf("publish-only measurement plan %d = %#v", index, entity)
		}
	}
}

// This test protects illuminance discovery as the sole lux measurement and
// fails if its kind, unit, envelope, identity, or read-only route drifts.
func TestDiscoverMeasurementIlluminanceRegistersLux(t *testing.T) {
	t.Parallel()
	discovered, rejection := discoverDevice(eligibleMeasurementDevice("illuminance", "lx", 1|4))
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if len(discovered.Entities) != 1 {
		t.Fatalf("Entities = %#v", discovered.Entities)
	}
	illuminance := discovered.Entities[0]
	wantSupport := `{"state":{"maximum":1000000000,"measurement_kind":"illuminance",` +
		`"minimum":0,"unit":"lx"},"operations":{}}`
	if illuminance.Descriptor.Key != "illuminance" ||
		illuminance.Descriptor.ExternalID != "0x00124b0024abcdef/root/illuminance" ||
		illuminance.Descriptor.Name != "Illuminance" || illuminance.Descriptor.Type != "hearth.measurement/v1" ||
		string(illuminance.Descriptor.Support) != wantSupport {
		t.Fatalf("illuminance descriptor = %#v", illuminance.Descriptor)
	}
	if !reflect.DeepEqual(illuminance.StateProperties, []string{"illuminance"}) ||
		!reflect.DeepEqual(illuminance.GetProperties, []string{"illuminance"}) ||
		illuminance.TranslateCommand != nil {
		t.Fatalf("gettable illuminance plan = %#v", illuminance)
	}
}

// This test protects endpoint-scoped measurement identity and fails if the
// planner ignores endpoint labels or custom property names.
func TestDiscoverMeasurementEndpointIdentity(t *testing.T) {
	t.Parallel()
	device := eligibleMeasurementDevice("humidity", "%", 1|4)
	device.Definition.Exposes[0].Property = "humidity_right"
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

// This test protects read-only access separation for every measurement kind
// and fails if a publish-only reading gains startup refresh, a gettable
// reading gains a command route, or a settable property still registers.
func TestMeasurementPlanSeparatesGetFromCommand(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ exposeName, unit string }{
		{exposeName: "temperature", unit: "°C"},
		{exposeName: "humidity", unit: "%"},
		{exposeName: "illuminance", unit: "lx"},
		{exposeName: "battery", unit: "%"},
	} {
		publishOnly, rejection := discoverDevice(eligibleMeasurementDevice(test.exposeName, test.unit, 1))
		if rejection != nil {
			t.Fatalf("%s Device rejected: %#v", test.exposeName, rejection)
		}
		if len(publishOnly.Entities) != 1 ||
			len(publishOnly.Entities[0].GetProperties) != 0 ||
			publishOnly.Entities[0].TranslateCommand != nil {
			t.Fatalf("publish-only %s plan = %#v", test.exposeName, publishOnly.Entities)
		}
		gettable, rejection := discoverDevice(eligibleMeasurementDevice(test.exposeName, test.unit, 1|4))
		if rejection != nil {
			t.Fatalf("%s Device rejected: %#v", test.exposeName, rejection)
		}
		if !reflect.DeepEqual(gettable.Entities[0].GetProperties, []string{test.exposeName}) ||
			gettable.Entities[0].TranslateCommand != nil {
			t.Fatalf("gettable %s plan = %#v", test.exposeName, gettable.Entities[0])
		}
		_, rejection = discoverDevice(eligibleMeasurementDevice(test.exposeName, test.unit, 1|2))
		if rejection == nil || rejection.Code != rejectionNoEligibleEntity {
			t.Fatalf("settable %s rejection = %#v", test.exposeName, rejection)
		}
	}
}

// This test protects the measurement eligibility gate and fails if unit,
// property, access, endpoint, or capability violations still register.
func TestMeasurementPlanEligibility(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*upstreamDevice)
	}{
		{name: "wrong unit", edit: func(device *upstreamDevice) {
			device.Definition.Exposes[0].Unit = "°F"
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
			duplicate.Unit = "%"
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
			device := eligibleMeasurementDevice("battery", "%", 1)
			test.edit(&device)
			_, rejection := discoverDevice(device)
			if rejection == nil || rejection.Code != rejectionNoEligibleEntity {
				t.Fatalf("rejection = %#v", rejection)
			}
		})
	}
}

// This test protects per-property sibling isolation across the migrated kinds
// and fails if a missing, non-numeric, or out-of-range reading suppresses
// valid siblings or produces synthetic State.
func TestDecodeMeasurementStateIsolation(t *testing.T) {
	t.Parallel()
	device := eligibleMeasurementSiblingsDevice(1)
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	entities := bindPlans(discovered.Entities)
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"temperature", "humidity", "battery"}) {
		t.Fatalf("Entity keys = %v", got)
	}
	receivedAt := time.Unix(1, 0).UTC()

	complete, issues, err := decodeDeviceState(
		[]byte(`{"temperature":22.6,"humidity":48.2,"battery":100}`),
		entities,
		receivedAt,
	)
	if err != nil || len(issues) != 0 || len(complete) != 3 ||
		string(complete[0].report.Observation.Value) != "22.6" ||
		string(complete[1].report.Observation.Value) != "48.2" ||
		string(complete[2].report.Observation.Value) != "100" {
		t.Fatalf("complete states = %#v, issues = %#v, err = %v", complete, issues, err)
	}
	if semantic, ok := complete[0].report.semantic.(float64); !ok || semantic != 22.6 {
		t.Fatalf("temperature semantic = %#v", complete[0].report.semantic)
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
		if string(states[0].report.Observation.Value) != "22.6" {
			t.Fatalf("invalid %s suppressed temperature: %#v", test.payload, states)
		}
		for index, property := range test.wantProblem {
			if !reflect.DeepEqual(stateIssues[index].Properties, []string{property}) {
				t.Fatalf("invalid %s issue %d = %#v", test.payload, index, stateIssues[index])
			}
		}
	}
}

// This test protects read-only reconciliation and fails if a measurement
// Entity creates a command route or a publish-only reading triggers startup
// refresh.
func TestReconcileSeparatesMeasurementRoutesFromRefresh(t *testing.T) {
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
			published := reconcileSensorDevice(t, eligibleMeasurementDevice("humidity", "%", test.access))
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

// This test protects pre-registration descriptor completeness and fails if a
// measurement plan built with empty metadata can pass validation: the shared
// constructor defers completeness to validateEntityPlans.
func TestMeasurementDecodeAndValidationSeams(t *testing.T) {
	t.Parallel()
	plan := mustMeasurementPlan(t, "relative_humidity")
	_, _, decodeErr := plan.DecodeState(
		"entity-humidity",
		map[string]json.RawMessage{"humidity": json.RawMessage(`"damp"`)},
		time.Unix(1, 0).UTC(),
	)
	if decodeErr == nil {
		t.Fatal("non-numeric humidity was accepted")
	}
	incomplete, err := newMeasurementPlan(
		adapter.EntityMetadata{}, "battery", measurementSupport(t, "battery_level"), false)
	if err != nil {
		t.Fatal(err)
	}
	if validateErr := validateEntityPlans([]entityPlan{incomplete}); validateErr == nil {
		t.Fatal("measurement plan with empty metadata passed validation")
	}
}

// FuzzMeasurementPlanBoundary protects the measurement facade boundary
// against malformed or extreme input and fails if the plan accepts a
// non-finite or out-of-range State or reports a semantic diverging from its
// own Observation.
func FuzzMeasurementPlanBoundary(fuzz *testing.F) {
	for _, seed := range []string{
		`21.5`, `2.15e1`, `-273.15`, `1000`, `-273.151`, `1000.001`, `0.0001`, `"21.5"`, `1e10000`, `null`,
	} {
		fuzz.Add(seed)
	}
	plan := mustMeasurementPlanForFuzz()
	receivedAt := time.Unix(1, 0).UTC()
	fuzz.Fuzz(func(t *testing.T, payload string) {
		report, decoded, planErr := plan.DecodeState(
			"entity-temperature",
			map[string]json.RawMessage{"temperature": json.RawMessage(payload)},
			receivedAt,
		)
		if planErr != nil || !decoded {
			return
		}
		value, ok := report.semantic.(float64)
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("payload %q produced non-finite semantic %#v", payload, report.semantic)
		}
		if value < -273.15 || value > 1000 {
			t.Fatalf("payload %q produced out-of-range semantic %v", payload, value)
		}
		var roundTripped float64
		if err := json.Unmarshal(report.Observation.Value, &roundTripped); err != nil || roundTripped != value {
			t.Fatalf("payload %q Observation %s round-tripped to %v, err=%v; want %v",
				payload, report.Observation.Value, roundTripped, err, value)
		}
	})
}

// mustMeasurementPlanForFuzz builds the fuzz target plan without a [testing.T].
func mustMeasurementPlanForFuzz() entityPlan {
	support := sdkmeasurementv1.Support{
		State: sdkmeasurementv1.StateSupport{
			MeasurementKind: "temperature", Unit: "Cel", Minimum: -273.15, Maximum: 1000,
		},
		Operations: sdkmeasurementv1.OperationSupport{},
	}
	plan, err := newMeasurementPlan(adapter.EntityMetadata{
		Key:        "temperature",
		ExternalID: "0x1/root/temperature",
		Name:       "Temperature",
	}, "temperature", support, false)
	if err != nil {
		panic(err)
	}
	return plan
}
