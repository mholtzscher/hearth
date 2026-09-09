package zigbee2mqtt //nolint:testpackage // Tests exercise package-private State decoding.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// This test protects exact XY scaling and rounding: raw 0..1 maps to
// round(raw*10000) half-up, and raw out-of-range values fail before rounding
// so no observation is clamped.
func TestDecodeScaledCoordinateXY(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		payload string
		want    int64
		valid   bool
	}{
		{payload: `0`, want: 0, valid: true},
		{payload: `0.3125`, want: 3125, valid: true},
		{payload: `0.3291`, want: 3291, valid: true},
		{payload: `1`, want: 10000, valid: true},
		{payload: `1.0`, want: 10000, valid: true},
		{payload: `0.31254`, want: 3125, valid: true},
		{payload: `0.31255`, want: 3126, valid: true},
		{payload: `0.00005`, want: 1, valid: true},
		{payload: `0.00004`, want: 0, valid: true},
		{payload: `-0.00001`},
		{payload: `1.00001`},
		{payload: `"0.5"`},
		{payload: `null`},
		{payload: `true`},
		{payload: `1e10000`},
		{payload: `{}`},
	} {
		got, err := decodeScaledCoordinate(json.RawMessage(test.payload), maxRawXY, colorXYScale)
		if (err == nil) != test.valid || got != test.want {
			t.Errorf(
				"decodeScaledCoordinate(%s) = %d, %v; want %d, valid=%t",
				test.payload,
				got,
				err,
				test.want,
				test.valid,
			)
		}
	}
}

// This test protects hue canonicalization and saturation bounds: hue accepts
// raw 0..360 with fractional rounding and folds 360 to 0, while saturation
// stays strict 0..100 with raw range checks before rounding.
func TestDecodeScaledCoordinateHueSaturation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		payload string
		maximum string
		want    int64
		valid   bool
	}{
		{payload: `0`, maximum: "hue", want: 0, valid: true},
		{payload: `120`, maximum: "hue", want: 120, valid: true},
		{payload: `359.4`, maximum: "hue", want: 359, valid: true},
		{payload: `359.6`, maximum: "hue", want: 360, valid: true},
		{payload: `360`, maximum: "hue", want: 360, valid: true},
		{payload: `360.4`, maximum: "hue"},
		{payload: `-1`, maximum: "hue"},
		{payload: `0`, maximum: "saturation", want: 0, valid: true},
		{payload: `80`, maximum: "saturation", want: 80, valid: true},
		{payload: `100`, maximum: "saturation", want: 100, valid: true},
		{payload: `79.5`, maximum: "saturation", want: 80, valid: true},
		{payload: `100.4`, maximum: "saturation"},
		{payload: `-0.5`, maximum: "saturation"},
	} {
		maximum := maxRawHue
		if test.maximum == "saturation" {
			maximum = maxRawSaturation
		}
		got, err := decodeScaledCoordinate(json.RawMessage(test.payload), maximum, 1)
		if (err == nil) != test.valid || got != test.want {
			t.Errorf(
				"decodeScaledCoordinate(%s) = %d, %v; want %d, valid=%t",
				test.payload,
				got,
				err,
				test.want,
				test.valid,
			)
		}
	}
}

// This test protects exact base-10 XY command encoding without float
// arithmetic, so no float-artifact digits reach the wire.
func TestFormatScaledUnit(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value int64
		want  string
	}{
		{value: 0, want: "0"},
		{value: 1, want: "0.0001"},
		{value: 10, want: "0.001"},
		{value: 100, want: "0.01"},
		{value: 1000, want: "0.1"},
		{value: 3125, want: "0.3125"},
		{value: 3291, want: "0.3291"},
		{value: 5000, want: "0.5"},
		{value: 9999, want: "0.9999"},
		{value: 10000, want: "1"},
	} {
		if got := formatScaledUnit(test.value); got != test.want {
			t.Errorf("formatScaledUnit(%d) = %q, want %q", test.value, got, test.want)
		}
	}
}

// This test protects mode-aware color decoding on a dual bulb: activity
// follows the same-message mode, complete but inactive values publish with
// active:false, and each representation decodes independently from one shared
// color object with extra fields harmless.
func TestDecodeDualColorActivity(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-color-dual.json")
	entities := bindPlans(device.Entities)
	receivedAt := time.Unix(1, 0).UTC()
	states, issues, err := decodeDeviceState(
		[]byte(`{"state":"ON","color":{"x":0.3125,"y":0.3291,"hue":120,"saturation":80},"color_mode":"xy"}`),
		entities,
		receivedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(states) != 4 {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
	byEntity := make(map[string]string, len(states))
	for _, state := range states {
		byEntity[state.entityID] = string(state.report.Observation.Value)
	}
	if byEntity["entity-power"] != "true" ||
		byEntity["entity-colorxy"] != `{"active":true,"x":3125,"y":3291}` ||
		byEntity["entity-colorhs"] != `{"active":false,"hue":120,"saturation":80}` {
		t.Fatalf("states = %v", byEntity)
	}
	// HS mode flips activity without changing the values.
	states, issues, err = decodeDeviceState(
		[]byte(`{"color":{"x":0.3125,"y":0.3291,"hue":120,"saturation":80},"color_mode":"hs"}`),
		entities,
		receivedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(states) != 3 {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
	byEntity = make(map[string]string, len(states))
	for _, state := range states {
		byEntity[state.entityID] = string(state.report.Observation.Value)
	}
	if byEntity["entity-colorxy"] != `{"active":false,"x":3125,"y":3291}` ||
		byEntity["entity-colorhs"] != `{"active":true,"hue":120,"saturation":80}` ||
		byEntity["entity-colormode"] != `"hs"` {
		t.Fatalf("states = %v", byEntity)
	}
}

// This test protects hue canonicalization end to end: observed 360 and 359.6
// decode to canonical zero in published State.
func TestDecodeHueCanonicalizes360(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-color-hs-only.json")
	entities := bindPlans(device.Entities)
	receivedAt := time.Unix(1, 0).UTC()
	for _, test := range []struct {
		payload string
		want    string
	}{
		{
			payload: `{"color":{"hue":360,"saturation":80},"color_mode":"hs"}`,
			want:    `{"active":true,"hue":0,"saturation":80}`,
		},
		{
			payload: `{"color":{"hue":359.6,"saturation":80},"color_mode":"hs"}`,
			want:    `{"active":true,"hue":0,"saturation":80}`,
		},
		{
			payload: `{"color":{"hue":359.4,"saturation":80},"color_mode":"hs"}`,
			want:    `{"active":true,"hue":359,"saturation":80}`,
		},
	} {
		states, issues, err := decodeDeviceState([]byte(test.payload), entities, receivedAt)
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 0 || len(states) != 2 || string(states[0].report.Observation.Value) != test.want {
			t.Fatalf("payload %s: states=%#v issues=%#v", test.payload, states, issues)
		}
	}
}

// This test protects per-representation pair completeness: a partial pair
// inside a present color object is invalid for that representation with valid
// siblings intact, while a missing top-level value or mode skips without an
// issue and manufactures no active:false.
//
//nolint:gocognit // One function keeps every pair-completeness case together.
func TestDecodeColorPairsSkipAndIsolate(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-color-dual.json")
	entities := bindPlans(device.Entities)
	receivedAt := time.Unix(1, 0).UTC()
	t.Run("partial XY pair issues XY only", func(t *testing.T) {
		t.Parallel()
		states, issues, err := decodeDeviceState(
			[]byte(`{"color":{"x":0.5,"hue":120,"saturation":80},"color_mode":"xy"}`),
			entities,
			receivedAt,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(states) != 2 || len(issues) != 1 ||
			!reflect.DeepEqual(issues[0].Properties, []string{"color", "color_mode"}) {
			t.Fatalf("states=%#v issues=%#v", states, issues)
		}
	})
	t.Run("missing top-level color skips without an issue", func(t *testing.T) {
		t.Parallel()
		states, issues, err := decodeDeviceState([]byte(`{"color_mode":"xy"}`), entities, receivedAt)
		if err != nil {
			t.Fatal(err)
		}
		if len(states) != 1 || states[0].entityID != "entity-colormode" || len(issues) != 0 {
			t.Fatalf("states=%#v issues=%#v", states, issues)
		}
	})
	t.Run("missing mode skips color without an issue", func(t *testing.T) {
		t.Parallel()
		states, issues, err := decodeDeviceState(
			[]byte(`{"color":{"x":0.5,"y":0.5}}`),
			entities,
			receivedAt,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(states) != 0 || len(issues) != 0 {
			t.Fatalf("states=%#v issues=%#v", states, issues)
		}
	})
	t.Run("mode-only message updates only mode", func(t *testing.T) {
		t.Parallel()
		states, issues, err := decodeDeviceState([]byte(`{"color_mode":"color_temp"}`), entities, receivedAt)
		if err != nil {
			t.Fatal(err)
		}
		if len(states) != 1 || states[0].entityID != "entity-colormode" ||
			string(states[0].report.Observation.Value) != `"color_temp"` || len(issues) != 0 {
			t.Fatalf("states=%#v issues=%#v", states, issues)
		}
	})
	t.Run("non-object color issues both representations", func(t *testing.T) {
		t.Parallel()
		states, issues, err := decodeDeviceState(
			[]byte(`{"color":"0.5","color_mode":"xy"}`),
			entities,
			receivedAt,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(states) != 1 || len(issues) != 2 {
			t.Fatalf("states=%#v issues=%#v", states, issues)
		}
	})
}

// This test protects strict mode decoding: unknown values are invalid
// observations, not a new State and not an inferred mode, while known values
// publish even when the matching representation is not advertised.
func TestDecodeColorModeStrict(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{`"xy"`, `"hs"`, `"color_temp"`} {
		decoded, err := decodeReportedColorMode(json.RawMessage(mode))
		if err != nil || string(decoded) != mode[1:len(mode)-1] {
			t.Errorf("decodeReportedColorMode(%s) = %q, %v", mode, decoded, err)
		}
	}
	for _, payload := range []string{`"XY"`, `"rgb"`, `""`, `null`, `0`, `true`, `{"mode":"xy"}`} {
		if _, err := decodeReportedColorMode(json.RawMessage(payload)); err == nil {
			t.Errorf("decodeReportedColorMode(%s) was accepted", payload)
		}
	}
	// Unknown mode issues every mode-sensitive Entity with valid siblings intact.
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-color-dual.json")
	states, issues, err := decodeDeviceState(
		[]byte(`{"state":"ON","color":{"x":0.5,"y":0.5},"color_temp":370,"color_mode":"rgb"}`),
		bindPlans(device.Entities),
		time.Unix(1, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || len(issues) != 4 {
		t.Fatalf("states=%#v issues=%#v", states, issues)
	}
	// Hearth publishes a reported known mode even when the matching
	// representation is not an advertised capability.
	xyOnly := mustDiscoveredFixtureDevice(t, "bridge-devices-color-xy-only.json")
	states, issues, err = decodeDeviceState(
		[]byte(`{"color_mode":"hs"}`),
		bindPlans(xyOnly.Entities),
		time.Unix(1, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || string(states[0].report.Observation.Value) != `"hs"` || len(issues) != 0 {
		t.Fatalf("states=%#v issues=%#v", states, issues)
	}
}

// This test protects mode-aware temperature decoding: activity derives from
// the same-message mode, an inactive exact match is a published inactive
// State, and malformed values never suppress valid siblings.
func TestDecodeTemperatureModeAware(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-color-dual.json")
	entities := bindPlans(device.Entities)
	receivedAt := time.Unix(1, 0).UTC()
	states, issues, err := decodeDeviceState(
		[]byte(`{"color_temp":370,"color_mode":"color_temp"}`),
		entities,
		receivedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(states) != 2 ||
		string(states[0].report.Observation.Value) != `{"active":true,"value":370}` {
		t.Fatalf("states=%#v issues=%#v", states, issues)
	}
	states, issues, err = decodeDeviceState(
		[]byte(`{"color_temp":370,"color_mode":"xy"}`),
		entities,
		receivedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(states) != 2 ||
		string(states[0].report.Observation.Value) != `{"active":false,"value":370}` {
		t.Fatalf("states=%#v issues=%#v", states, issues)
	}
}

// This test protects the temperature-only fallback: temperature without an
// advertised color composite stays active when mode is absent, still derives
// activity when mode is present, and issues on a malformed mode.
func TestDecodeTemperatureOnlyFallback(t *testing.T) {
	t.Parallel()
	device := eligibleDevice()
	device.Definition.Exposes = []upstreamExpose{
		colorLightExpose("", "state", "brightness", colorTempFeature("color_temp", 153, 500)),
	}
	discovered, rejection := discoverDevice(device, mustEmbeddedProfileCatalog(t))
	if rejection != nil {
		t.Fatal(rejection)
	}
	entities := bindPlans(discovered.Entities)
	receivedAt := time.Unix(1, 0).UTC()
	states, issues, err := decodeDeviceState([]byte(`{"color_temp":370}`), entities, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(states) != 1 ||
		string(states[0].report.Observation.Value) != `{"active":true,"value":370}` {
		t.Fatalf("states=%#v issues=%#v", states, issues)
	}
	states, issues, err = decodeDeviceState(
		[]byte(`{"color_temp":370,"color_mode":"hs"}`),
		entities,
		receivedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(states) != 2 ||
		string(states[0].report.Observation.Value) != `{"active":false,"value":370}` ||
		string(states[1].report.Observation.Value) != `"hs"` {
		t.Fatalf("states=%#v issues=%#v", states, issues)
	}
	states, issues, err = decodeDeviceState(
		[]byte(`{"color_temp":370,"color_mode":"rgb"}`),
		entities,
		receivedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 || len(issues) != 2 {
		t.Fatalf("states=%#v issues=%#v", states, issues)
	}
}

// This test protects malformed XY isolation: malformed XY never blocks valid
// HS, temperature, power, or mode siblings.
func TestDecodeMalformedXYIsolatesSiblings(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-color-dual.json")
	states, issues, err := decodeDeviceState(
		[]byte(
			`{"state":"ON","color":{"x":"0.5","y":0.5,"hue":120,"saturation":80},"color_temp":370,"color_mode":"hs"}`,
		),
		bindPlans(device.Entities),
		time.Unix(1, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	byEntity := make(map[string]string, len(states))
	for _, state := range states {
		byEntity[state.entityID] = string(state.report.Observation.Value)
	}
	if len(issues) != 1 || len(states) != 4 ||
		byEntity["entity-colorhs"] != `{"active":true,"hue":120,"saturation":80}` ||
		byEntity["entity-colortemp"] != `{"active":false,"value":370}` {
		t.Fatalf("states=%v issues=%#v", byEntity, issues)
	}
}
