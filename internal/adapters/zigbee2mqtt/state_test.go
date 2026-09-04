package zigbee2mqtt //nolint:testpackage // Tests exercise package-private State and command translation.

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// This test protects captured typed State projection and fails on integer-only brightness decoding, hard-coded ON/OFF
// routing, color-temperature conversion, or a brightness normalization formula other than nearest integer percent.
func TestDecodeCapturedFractionalState(t *testing.T) {
	t.Parallel()
	discovery, err := discoverInventory(readFixture(t, "bridge-devices-3rcb01057z.json"))
	if err != nil {
		t.Fatal(err)
	}
	receivedAt := time.Unix(1, 0).UTC()
	states, issues, err := decodeDeviceState(
		readFixture(t, "state-3rcb01057z.json"),
		bindPlans(discovery.Devices[0].Entities),
		receivedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(states) != 3 ||
		states[0].entityID != "entity-power" || string(states[0].report.Observation.Value) != "true" ||
		states[1].entityID != "entity-brightness" || string(states[1].report.Observation.Value) != "25" ||
		states[2].entityID != "entity-colortemp" || string(states[2].report.Observation.Value) != "370" {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
	if states[2].report.Observation.EntityID != "entity-colortemp" ||
		states[2].report.Observation.AdapterReceivedAt != receivedAt.Format(time.RFC3339Nano) {
		t.Fatalf("color-temperature Observation = %#v", states[2].report.Observation)
	}
}

// This test protects independent property projection and fails if an invalid brightness suppresses a valid power sibling,
// unknown properties produce State, or absent properties are inferred.
func TestDecodeDeviceStateIsolatesInvalidAndUnknownProperties(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rcb01057z.json")
	entities := bindPlans(device.Entities)
	receivedAt := time.Unix(1, 0).UTC()
	states, issues, err := decodeDeviceState(
		[]byte(`{"state":"OFF","brightness":"64","unknown":true}`),
		entities,
		receivedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || string(states[0].report.Observation.Value) != "false" || len(issues) != 1 ||
		!reflect.DeepEqual(issues[0].Properties, []string{"brightness"}) {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
	states, issues, err = decodeDeviceState([]byte(`{"brightness":127.5}`), entities, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].entityID != "entity-brightness" ||
		string(states[0].report.Observation.Value) != "50" || len(issues) != 0 {
		t.Fatalf("brightness-only states = %#v, issues = %#v", states, issues)
	}
}

// This test protects strict color-temperature decoding and sibling issue isolation. It fails on conversion, clamping,
// fractional rounding, numeric-string coercion, or whole-message rejection for one invalid recognized property.
func TestNormalizeColorTempAndIsolateInvalidProperty(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		payload string
		want    int64
	}{
		{payload: `154`, want: 154},
		{payload: `370.0`, want: 370},
		{payload: `500`, want: 500},
	} {
		value, err := normalizeColorTemp(json.RawMessage(test.payload), 154, 500)
		if err != nil || value != test.want {
			t.Errorf("normalizeColorTemp(%s) = %d, %v; want %d", test.payload, value, err, test.want)
		}
	}
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rcb01057z.json")
	entities := bindPlans(device.Entities)
	receivedAt := time.Unix(1, 0).UTC()
	for _, payload := range []string{`153`, `501`, `370.5`, `"370"`, `1e10000`} {
		states, issues, err := decodeDeviceState(
			[]byte(`{"state":"OFF","color_temp":`+payload+`}`),
			entities,
			receivedAt,
		)
		if err != nil {
			t.Fatalf("decode sibling State with color_temp %s: %v", payload, err)
		}
		if len(states) != 1 || string(states[0].report.Observation.Value) != "false" || len(issues) != 1 ||
			!reflect.DeepEqual(issues[0].Properties, []string{"color_temp"}) {
			t.Errorf("color_temp %s: states=%#v issues=%#v", payload, states, issues)
		}
	}
}

// This test protects canonical scalar comparison and fails if JSON number spelling or whitespace changes power meaning,
// or if structured values are accepted as power scalars.
func TestDecodePowerUsesCanonicalJSONScalars(t *testing.T) {
	t.Parallel()
	on, err := canonicalScalar(json.RawMessage(`1.0`))
	if err != nil {
		t.Fatal(err)
	}
	off, err := canonicalScalar(json.RawMessage(`0`))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		payload string
		want    bool
		valid   bool
	}{
		{payload: `1e0`, want: true, valid: true},
		{payload: ` -0.0 `, want: false, valid: true},
		{payload: `2`},
		{payload: `"1"`},
		{payload: `{}`},
		{payload: `[]`},
	} {
		got, decodeErr := decodePowerState(json.RawMessage(test.payload), on, off)
		if (decodeErr == nil) != test.valid || got != test.want {
			t.Errorf(
				"decodePowerState(%s) = %t, %v; want %t, valid=%t",
				test.payload,
				got,
				decodeErr,
				test.want,
				test.valid,
			)
		}
	}
}

// This test protects brightness input boundaries and fails on numeric-string coercion, clamping, overflow-to-infinity,
// truncation instead of rounding, or acceptance of malformed JSON.
func TestNormalizeBrightnessValidationAndRounding(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		payload string
		maximum float64
		want    int64
		valid   bool
	}{
		{payload: `0`, maximum: 255, want: 0, valid: true},
		{payload: `1.275`, maximum: 255, want: 1, valid: true},
		{payload: `63.75`, maximum: 255, want: 25, valid: true},
		{payload: `254`, maximum: 255, want: 100, valid: true},
		{payload: `255.0`, maximum: 255, want: 100, valid: true},
		{payload: `-0.01`, maximum: 255},
		{payload: `255.01`, maximum: 255},
		{payload: `"63.75"`, maximum: 255},
		{payload: `1e10000`, maximum: 255},
		{payload: `null`, maximum: 255},
		{payload: `63.75 trailing`, maximum: 255},
		{payload: `1`, maximum: math.NaN()},
		{payload: `1`, maximum: math.Inf(1)},
		{payload: `0`, maximum: 0},
	} {
		got, err := normalizeBrightness(json.RawMessage(test.payload), test.maximum)
		if (err == nil) != test.valid || got != test.want {
			t.Errorf(
				"normalizeBrightness(%s, %v) = %d, %v; want %d, valid=%t",
				test.payload,
				test.maximum,
				got,
				err,
				test.want,
				test.valid,
			)
		}
	}
}

// This exhaustive test protects every legal Hearth command percentage and fails on lossy scaling, integer-only JSON,
// endpoint maximum assumptions, or non-inverse observation normalization.
func TestBrightnessCommandRoundTripExhaustive(t *testing.T) {
	t.Parallel()
	for _, maximum := range []float64{100, 101, 254, 255, 1000, 65535} {
		for percentage := range int64(101) {
			encoded, err := scaleBrightnessCommand(percentage, maximum)
			if err != nil {
				t.Fatalf("scale %d/%v: %v", percentage, maximum, err)
			}
			got, err := normalizeBrightness(encoded, maximum)
			if err != nil {
				t.Fatalf("normalize %d/%v from %s: %v", percentage, maximum, encoded, err)
			}
			if got != percentage {
				t.Fatalf("round trip %d/%v from %s = %d", percentage, maximum, encoded, got)
			}
		}
	}
	encoded, err := scaleBrightnessCommand(25, 255)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "63.75" {
		t.Fatalf("25%% of 255 = %s, want 63.75", encoded)
	}
}

// This property test protects round trips over a broad range of legal maxima and fails on floating-point order defects.
func TestBrightnessCommandRoundTripProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		maximum := float64(rapid.Int64Range(100, 1_000_000_000).Draw(t, "maximum"))
		percentage := rapid.Int64Range(0, 100).Draw(t, "percentage")
		encoded, err := scaleBrightnessCommand(percentage, maximum)
		if err != nil {
			t.Fatal(err)
		}
		got, err := normalizeBrightness(encoded, maximum)
		if err != nil {
			t.Fatal(err)
		}
		if got != percentage {
			t.Fatalf("round trip %d/%v from %s = %d", percentage, maximum, encoded, got)
		}
	})
}

// This test protects typed command routing and fails if power values are hard-coded or command percentage bounds are skipped.
func TestCommandValuesUseDiscoveredMetadata(t *testing.T) {
	t.Parallel()
	onScalar, err := canonicalScalar(json.RawMessage(`1`))
	if err != nil {
		t.Fatal(err)
	}
	offScalar, err := canonicalScalar(json.RawMessage(`0`))
	if err != nil {
		t.Fatal(err)
	}
	on := powerCommandValue(onScalar, offScalar, true)
	off := powerCommandValue(onScalar, offScalar, false)
	quarter, err := brightnessCommandValue(255, 25)
	if err != nil {
		t.Fatal(err)
	}
	if string(on) != "1" || string(off) != "0" || string(quarter) != "63.75" {
		t.Fatalf("command values = on:%s off:%s brightness:%s", on, off, quarter)
	}
	if _, err = brightnessCommandValue(255, -1); err == nil {
		t.Fatal("negative brightness command was accepted")
	}
	if _, err = brightnessCommandValue(255, 101); err == nil {
		t.Fatal("brightness command above 100 was accepted")
	}
	if _, err = brightnessCommandValue(0, 0); err == nil {
		t.Fatal("zero brightness maximum was accepted")
	}
}

// This test protects malformed top-level State rejection.
func TestDecodeDeviceStateRejectsInvalidShape(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rcb01057z.json")
	for _, payload := range [][]byte{[]byte(`null`), []byte(`[]`), []byte(`{`), []byte(`{} {}`)} {
		if _, _, err := decodeDeviceState(payload, bindPlans(device.Entities), time.Unix(1, 0).UTC()); err == nil {
			t.Errorf("decodeDeviceState(%q) accepted invalid shape", payload)
		}
	}
}

// FuzzDeviceState protects parser stability, sibling isolation, and bounded typed output.
func FuzzDeviceState(fuzz *testing.F) {
	device := mustDiscoveredFixtureDevice(fuzz, "bridge-devices-3rcb01057z.json")
	fuzz.Add(readFixture(fuzz, "state-3rcb01057z.json"))
	fuzz.Add([]byte(`{"brightness":1e10000,"state":"OFF"}`))
	fuzz.Add([]byte(`{}`))
	entities := bindPlans(device.Entities)
	fuzz.Fuzz(func(t *testing.T, payload []byte) {
		states, _, err := decodeDeviceState(payload, entities, time.Unix(1, 0).UTC())
		if err != nil {
			return
		}
		seen := make(map[string]struct{}, len(states))
		for _, state := range states {
			if _, duplicate := seen[state.entityID]; duplicate {
				t.Fatalf("duplicate projected Entity %q", state.entityID)
			}
			seen[state.entityID] = struct{}{}
			if state.entityID == "" || len(state.report.Observation.Value) == 0 {
				t.Fatalf("projected incomplete State %#v", state)
			}
		}
	})
}

func TestDecodeStatePreservesDiscoveryOrder(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rcb01057z.json")
	states, issues, err := decodeDeviceState(
		[]byte(`{"color_temp":370,"brightness":254,"state":"ON"}`),
		bindPlans(device.Entities),
		time.Unix(1, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 3 || len(issues) != 0 {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
	got := []string{states[0].entityID, states[1].entityID, states[2].entityID}
	if !reflect.DeepEqual(got, []string{"entity-power", "entity-brightness", "entity-colortemp"}) {
		t.Fatalf("State order = %v", got)
	}
}
