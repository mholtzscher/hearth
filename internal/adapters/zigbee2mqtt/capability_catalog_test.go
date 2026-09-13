package zigbee2mqtt //nolint:testpackage // Exercise private capability mapping through production planning helpers.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// A new diagnostic numeric capability needs only data, not a strategy
// implementation. This synthetic pressure mapping must preserve fractions and
// exact units, use the MQTT property rather than the expose name, and stay
// read-only. It is test-only: it does not claim support for an uncaptured
// device.
func TestNumericSensorMappingAddsCapabilityWithoutNewTranslation(t *testing.T) {
	t.Parallel()
	mapping := numericSensorMapping{
		exposeName: "pressure", key: "airpressure", displayName: "Air Pressure",
		upstreamUnit: "hPa", unit: "hPa", minimum: 300, maximum: 1100,
	}
	device := eligibleSensorDevice("temperature", 1)
	device.Definition.Exposes = []upstreamExpose{
		{Type: "numeric", Name: "pressure", Property: "pressure_report", Unit: "hPa", Access: 5},
	}
	input := devicePlanningInput{IEEE: device.IEEEAddress, Exposes: newExposeIndex(device)}
	plan := planNumericSensorRoot(input, input.Exposes.roots[0], mapping)
	if plan == nil {
		t.Fatal("new numeric capability mapping was not planned")
	}
	if plan.Descriptor.Key != "airpressure" || plan.Descriptor.Name != "Air Pressure" ||
		plan.Descriptor.ExternalID != "0x00124b0024abcdef/root/airpressure" ||
		plan.Descriptor.Type != "hearth.numericsensor/v1" ||
		string(plan.Descriptor.Support) != `{"state":{"maximum":1100,"minimum":300,"unit":"hPa"},"operations":{}}` {
		t.Fatalf("pressure descriptor = %#v", plan.Descriptor)
	}
	if !reflect.DeepEqual(plan.StateProperties, []string{"pressure_report"}) ||
		!reflect.DeepEqual(plan.GetProperties, []string{"pressure_report"}) || plan.TranslateCommand != nil {
		t.Fatalf("pressure routes = %#v", plan)
	}
	if report := contractDecode(t, *plan, "pressure_report", `1013.25`); report.semantic != 1013.25 {
		t.Fatalf("pressure = %v, want 1013.25", report.semantic)
	}
	for _, payload := range []string{`299.9`, `1100.1`, `"1013.25"`} {
		if _, _, err := plan.DecodeState("pressure", map[string]json.RawMessage{
			"pressure_report": json.RawMessage(payload),
		}, time.Unix(1, 0)); err == nil {
			t.Fatalf("pressure accepted invalid payload %s", payload)
		}
	}
	// No implicit catch-all: production discovery does not support this
	// synthetic capability until an explicit catalog entry is added.
	if _, rejection := discoverDevice(device); rejection == nil {
		t.Fatal("unknown pressure capability registered without a catalog entry")
	}
	for _, expose := range []upstreamExpose{
		{Type: "numeric", Name: "pressure", Property: "pressure_report", Unit: "Pa", Access: 5},
		{Type: "numeric", Name: "pressure", Property: "pressure_report", Unit: "hPa", Access: 7},
		{Type: "numeric", Name: "pressure", Property: "pressure_report", Unit: "hPa", Access: 4},
		{Type: "numeric", Name: "unknown", Property: "pressure_report", Unit: "hPa", Access: 5},
	} {
		device.Definition.Exposes = []upstreamExpose{expose}
		input.Exposes = newExposeIndex(device)
		if planNumericSensorRoot(input, input.Exposes.roots[0], mapping) != nil {
			t.Fatalf("ineligible pressure expose registered: %#v", expose)
		}
	}
}

// A new binary capability needs only one binarySensorMappings record, not new
// translation code. This synthetic contact mapping must take its State
// property, endpoint, and declared scalar pair from inventory, stay
// read-only, and invert the boolean meaning via the declaration. It is
// test-only: production discovery must not support contact until an explicit
// catalog record exists.
func TestBinarySensorMappingAddsCapabilityWithoutNewTranslation(t *testing.T) {
	t.Parallel()
	mapping := binarySensorMapping{exposeName: "contact", key: "contact", displayName: "Contact"}
	// value_on false / value_off true is an inverted declaration: raw false
	// maps to Hearth true. Tests use it so an assumed boolean would fail.
	device := eligibleSensorDevice("temperature", 1)
	device.Definition.Exposes = []upstreamExpose{{
		Type: "binary", Name: "contact", Property: "contact_state", Access: 1,
		ValueOn: json.RawMessage(`false`), ValueOff: json.RawMessage(`true`),
	}}
	input := devicePlanningInput{IEEE: device.IEEEAddress, Exposes: newExposeIndex(device)}
	contribution := plannerContribution{Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental}
	appendBinarySensorPlans(&contribution, input, mapping)
	if len(contribution.Entities) != 1 {
		t.Fatalf("contact plans = %d, want one", len(contribution.Entities))
	}
	plan := contribution.Entities[0]
	if plan.Descriptor.Key != "contact" || plan.Descriptor.Name != "Contact" ||
		plan.Descriptor.ExternalID != "0x00124b0024abcdef/root/contact" ||
		plan.Descriptor.Type != "hearth.binarysensor/v1" ||
		string(plan.Descriptor.Support) != `{"state":{},"operations":{}}` {
		t.Fatalf("contact descriptor = %#v", plan.Descriptor)
	}
	// The inventory property alias, not the expose name, is the State route.
	if !reflect.DeepEqual(plan.StateProperties, []string{"contact_state"}) ||
		len(plan.GetProperties) != 0 || plan.TranslateCommand != nil {
		t.Fatalf("contact routes = %#v", plan)
	}
	// The declared pair, not an assumed boolean, defines the mapping: raw
	// false matches value_on and raw true matches value_off.
	if report := contractDecode(t, plan, "contact_state", `false`); report.semantic != true {
		t.Fatalf("declared value_on = %v, want true", report.semantic)
	}
	if report := contractDecode(t, plan, "contact_state", `true`); report.semantic != false {
		t.Fatalf("declared value_off = %v, want false", report.semantic)
	}
	// The declaration is the only source of the mapping: an undeclared
	// scalar must be an error, not a coerced truthiness.
	if _, _, err := plan.DecodeState("contact", map[string]json.RawMessage{
		"contact_state": json.RawMessage(`1`),
	}, time.Unix(1, 0)); err == nil {
		t.Fatal("contact accepted a scalar outside its declared pair")
	}
	// Get access alone controls refresh and never creates a command route.
	device.Definition.Exposes[0].Access = 5
	input.Exposes = newExposeIndex(device)
	gettable := plannerContribution{Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental}
	appendBinarySensorPlans(&gettable, input, mapping)
	if len(gettable.Entities) != 1 ||
		!reflect.DeepEqual(gettable.Entities[0].GetProperties, []string{"contact_state"}) ||
		gettable.Entities[0].TranslateCommand != nil {
		t.Fatalf("gettable contact plan = %#v", gettable.Entities)
	}
	// No implicit catch-all: production discovery does not support this
	// synthetic capability until an explicit catalog entry is added.
	if _, rejection := discoverDevice(device); rejection == nil {
		t.Fatal("unknown contact capability registered without a catalog entry")
	}
	for _, expose := range []upstreamExpose{
		// Get-only publish shape cannot report State.
		{Type: "binary", Name: "contact", Property: "contact_state", Access: 4,
			ValueOn: json.RawMessage(`false`), ValueOff: json.RawMessage(`true`)},
		// Set access means the property is controllable, not a sensor.
		{Type: "binary", Name: "contact", Property: "contact_state", Access: 3,
			ValueOn: json.RawMessage(`false`), ValueOff: json.RawMessage(`true`)},
		// Empty State property leaves no MQTT route.
		{Type: "binary", Name: "contact", Property: "", Access: 1,
			ValueOn: json.RawMessage(`false`), ValueOff: json.RawMessage(`true`)},
		// Malformed and indistinct declarations must not guess a mapping.
		{Type: "binary", Name: "contact", Property: "contact_state", Access: 1,
			ValueOn: json.RawMessage(`false`)},
		{Type: "binary", Name: "contact", Property: "contact_state", Access: 1,
			ValueOn: json.RawMessage(`true`), ValueOff: json.RawMessage(`true`)},
		{Type: "binary", Name: "contact", Property: "contact_state", Access: 1,
			ValueOn: json.RawMessage(`{"on":true}`), ValueOff: json.RawMessage(`false`)},
		// The record name, not the property, selects the capability.
		{Type: "binary", Name: "leak", Property: "contact_state", Access: 1,
			ValueOn: json.RawMessage(`false`), ValueOff: json.RawMessage(`true`)},
	} {
		device.Definition.Exposes = []upstreamExpose{expose}
		input.Exposes = newExposeIndex(device)
		contribution.Entities = nil
		appendBinarySensorPlans(&contribution, input, mapping)
		if len(contribution.Entities) != 0 {
			t.Fatalf("ineligible contact expose registered: %#v", expose)
		}
	}
}

// The captured 3RSNL02043Z night light is the sole binary capability in the
// catalog. This test pins that record to the live expose name, Entity key,
// and display name independently of the device fixture, and fails if a second
// occupancy record appears or any field drifts.
func TestBinarySensorMappingsMapCapturedOccupancy(t *testing.T) {
	t.Parallel()
	matches := 0
	var got binarySensorMapping
	for _, mapping := range binarySensorMappings() {
		if mapping.exposeName == "occupancy" {
			matches++
			got = mapping
		}
	}
	if matches != 1 {
		t.Fatalf("occupancy catalog entries = %d, want exactly one", matches)
	}
	want := binarySensorMapping{exposeName: "occupancy", key: "occupancy", displayName: "Occupancy"}
	if got != want {
		t.Fatalf("occupancy mapping = %#v, want %#v", got, want)
	}
}

// A new relay numeric setting needs only a mapping, while its actual bounds
// and MQTT property come from inventory. This test catches hard-coded units,
// properties, bounds, or integer-only translation in the shared constructor.
func TestNumericSettingMappingAddsCapabilityWithoutNewTranslation(t *testing.T) {
	t.Parallel()
	device := eligibleSensorDevice("temperature", 1)
	minimum, maximum := -5.0, 5.0
	device.Definition.Exposes = []upstreamExpose{
		{Type: "numeric", Name: "calibration_offset", Property: "offset_wire", Unit: "°C", Access: 7,
			ValueMin: &minimum, ValueMax: &maximum},
	}
	mapping := numericSettingMapping{
		exposeName: "calibration_offset", key: "calibrationoffset",
		displayName: "Calibration Offset", expectedUnit: "°C",
	}
	input := devicePlanningInput{IEEE: device.IEEEAddress, Exposes: newExposeIndex(device)}
	plan := planNumericSetting(input, mapping)
	if plan == nil {
		t.Fatal("new numeric setting mapping was not planned")
	}
	if plan.Descriptor.Key != "calibrationoffset" || plan.Descriptor.Name != "Calibration Offset" ||
		plan.Descriptor.ExternalID != "0x00124b0024abcdef/root/calibrationoffset" ||
		plan.Descriptor.Type != "hearth.numericsetting/v1" ||
		string(plan.Descriptor.Support) !=
			`{"state":{"choices":[],"maximum":5,"minimum":-5,"unit":"°C"},"operations":{"set":{}}}` {
		t.Fatalf("setting descriptor = %#v", plan.Descriptor)
	}
	wire, command := contractTranslate(t, *plan, "set", `{"mode":"value","value":-1.25}`)
	if string(wire) != `{"offset_wire":-1.25}` || !reflect.DeepEqual(command.GetProperties, []string{"offset_wire"}) {
		t.Fatalf("setting command = %s, get = %v", wire, command.GetProperties)
	}
	if !command.Matches(contractDecode(t, *plan, "offset_wire", `-1.25`)) ||
		command.Matches(contractDecode(t, *plan, "offset_wire", `-1`)) {
		t.Fatal("setting matcher did not preserve fractional target")
	}
	// Exact units remain an eligibility requirement, not a scale guess.
	device.Definition.Exposes[0].Unit = "°F"
	input.Exposes = newExposeIndex(device)
	if planNumericSetting(input, mapping) != nil {
		t.Fatal("setting accepted an incompatible unit")
	}
	// Unitless settings omit the optional support unit, not an empty string.
	mapping.expectedUnit = ""
	device.Definition.Exposes[0].Unit = ""
	input.Exposes = newExposeIndex(device)
	unitless := planNumericSetting(input, mapping)
	if unitless == nil || string(unitless.Descriptor.Support) !=
		`{"state":{"choices":[],"maximum":5,"minimum":-5},"operations":{"set":{}}}` {
		t.Fatalf("unitless setting = %#v", unitless)
	}
}
