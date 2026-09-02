package zigbee2mqtt //nolint:testpackage // Tests exercise package-private State and command translation.

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"pgregory.net/rapid"
)

// This test protects captured fractional State projection and fails on integer-only decoding, hard-coded ON/OFF routing,
// color leakage, or a normalization formula other than nearest integer percent.
func TestDecodeCapturedFractionalState(t *testing.T) {
	t.Parallel()
	discovery, err := discoverInventory(readFixture(t, "bridge-devices-3rcb01057z.json"))
	if err != nil {
		t.Fatal(err)
	}
	states, issues, err := decodeDeviceState(readFixture(t, "state-3rcb01057z.json"), discovery.Devices[0].Entities)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(states) != 2 || !states[0].Power || states[1].Brightness != 25 {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
}

// This test protects independent property projection and fails if an invalid brightness suppresses a valid power sibling,
// unknown properties produce State, or absent properties are inferred.
func TestDecodeDeviceStateIsolatesInvalidAndUnknownProperties(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rcb01057z.json")
	states, issues, err := decodeDeviceState(
		[]byte(`{"state":"OFF","brightness":"64","unknown":true}`),
		device.Entities,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].Power || len(issues) != 1 || issues[0].Property != "brightness" {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
	states, issues, err = decodeDeviceState([]byte(`{"brightness":127.5}`), device.Entities)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].Entity.Kind != entityKindBrightness || states[0].Brightness != 50 ||
		len(issues) != 0 {
		t.Fatalf("brightness-only states = %#v, issues = %#v", states, issues)
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
	device := mustDiscoveredFixtureDevice(t, "multi-endpoint-light.json")
	power := device.Entities[2]
	brightness := device.Entities[3]
	on, err := powerCommandValue(power, true)
	if err != nil {
		t.Fatal(err)
	}
	off, err := powerCommandValue(power, false)
	if err != nil {
		t.Fatal(err)
	}
	quarter, err := brightnessCommandValue(brightness, 25)
	if err != nil {
		t.Fatal(err)
	}
	if string(on) != "1" || string(off) != "0" || string(quarter) != "63.75" {
		t.Fatalf("command values = on:%s off:%s brightness:%s", on, off, quarter)
	}
	if _, err = brightnessCommandValue(brightness, -1); err == nil {
		t.Fatal("negative brightness command was accepted")
	}
	if _, err = brightnessCommandValue(brightness, 101); err == nil {
		t.Fatal("brightness command above 100 was accepted")
	}
	brightness.BrightnessMaximum = 0
	if _, err = brightnessCommandValue(brightness, 0); err == nil {
		t.Fatal("zero brightness maximum was accepted")
	}
	if _, err = powerCommandValue(brightness, true); err == nil {
		t.Fatal("brightness route was accepted for a power command")
	}
}

// This test protects explicit availability evidence and fails if unknown strings or malformed payloads imply availability.
func TestDecodeAvailability(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		payload string
		want    bool
		valid   bool
	}{
		{payload: `{"state":"online","extra":true}`, want: true, valid: true},
		{payload: `{"state":"offline"}`, want: false, valid: true},
		{payload: `{"state":"unknown"}`},
		{payload: `{}`},
		{payload: `null`},
		{payload: `not-json`},
	} {
		got, err := decodeAvailability([]byte(test.payload))
		if (err == nil) != test.valid || got != test.want {
			t.Errorf(
				"decodeAvailability(%s) = %t, %v; want %t, valid=%t",
				test.payload,
				got,
				err,
				test.want,
				test.valid,
			)
		}
	}
}

// This test protects malformed top-level State rejection.
func TestDecodeDeviceStateRejectsInvalidShape(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rcb01057z.json")
	for _, payload := range [][]byte{[]byte(`null`), []byte(`[]`), []byte(`{`), []byte(`{} {}`)} {
		if _, _, err := decodeDeviceState(payload, device.Entities); err == nil {
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
	fuzz.Fuzz(func(t *testing.T, payload []byte) {
		states, _, err := decodeDeviceState(payload, device.Entities)
		if err != nil {
			return
		}
		seen := make(map[string]struct{}, len(states))
		for _, state := range states {
			key := state.Entity.Descriptor.Key
			if _, duplicate := seen[key]; duplicate {
				t.Fatalf("duplicate projected Entity %q", key)
			}
			seen[key] = struct{}{}
			if state.Entity.Kind == entityKindBrightness && (state.Brightness < 0 || state.Brightness > 100) {
				t.Fatalf("brightness State = %d", state.Brightness)
			}
		}
	})
}

func mustDiscoveredFixtureDevice(testingT interface {
	Helper()
	Fatal(...any)
}, fixture string) discoveredDevice {
	testingT.Helper()
	result, err := discoverInventory(readFixture(testingT, fixture))
	if err != nil {
		testingT.Fatal(err)
	}
	if len(result.Devices) != 1 {
		testingT.Fatal("fixture did not discover exactly one Device")
	}
	return result.Devices[0]
}

func TestDecodeStatePreservesDiscoveryOrder(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rcb01057z.json")
	states, issues, err := decodeDeviceState([]byte(`{"brightness":255,"state":"ON"}`), device.Entities)
	if err != nil {
		t.Fatal(err)
	}
	got := []entityKind{states[0].Entity.Kind, states[1].Entity.Kind}
	if len(issues) != 0 || !reflect.DeepEqual(got, []entityKind{entityKindPower, entityKindBrightness}) {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
}
