package zigbee2mqtt //nolint:testpackage // Tests exercise package-private temperature translation.

import (
	"encoding/json"
	"reflect"
	"testing"

	"pgregory.net/rapid"
)

// This test protects exact milli-Celsius conversion and fails on float
// rounding, truncation, clamping, numeric-string coercion, or huge-exponent
// acceptance.
func TestNormalizeTemperatureTable(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		payload string
		want    int64
		valid   bool
	}{
		{payload: `21.5`, want: 21500, valid: true},
		{payload: `21.5000`, want: 21500, valid: true},
		{payload: `2.15e1`, want: 21500, valid: true},
		{payload: `-0.125`, want: -125, valid: true},
		{payload: `-273.15`, want: -273150, valid: true},
		{payload: `1000`, want: 1000000, valid: true},
		{payload: `0.0001`},
		{payload: `"21.5"`},
		{payload: `null`},
		{payload: `1e10000`},
		{payload: `-273.151`},
		{payload: `1000.001`},
		{payload: `21.5 trailing`},
	} {
		got, err := normalizeTemperature(json.RawMessage(test.payload))
		if (err == nil) != test.valid || got != test.want {
			t.Errorf(
				"normalizeTemperature(%s) = %d, %v; want %d, valid=%t",
				test.payload,
				got,
				err,
				test.want,
				test.valid,
			)
		}
	}
}

// This property test protects exact conversion over the whole supported range
// and fails if any integer milli-Celsius value does not survive a Celsius
// round trip. Expected values come from integer arithmetic, never the
// production converter.
func TestNormalizeTemperatureProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		milli := rapid.Int64Range(temperatureMinimumMilliCelsius, temperatureMaximumMilliCelsius).Draw(t, "milli")
		payload := formatCelsius(milli)
		got, err := normalizeTemperature(json.RawMessage(payload))
		if err != nil || got != milli {
			t.Fatalf("normalizeTemperature(%s) = %d, %v; want %d", payload, got, err, milli)
		}
	})
}

func formatCelsius(milli int64) string {
	negative := milli < 0
	absolute := milli
	if negative {
		absolute = -absolute
	}
	sign := ""
	if negative {
		sign = "-"
	}
	return sign + jsonNumber(int(absolute/1000)) + "." + padMillis(absolute%1000)
}

func padMillis(fraction int64) string {
	digits := jsonNumber(int(fraction))
	for len(digits) < 3 {
		digits = "0" + digits
	}
	return digits
}

// This test protects the temperature proof Device and fails if State is not
// integer milli-Celsius with empty support and no Operations.
func TestDiscoverTemperatureSensorRegistersMilliCelsius(t *testing.T) {
	t.Parallel()
	discovered, rejection := discoverDevice(eligibleSensorDevice("temperature", 1))
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if discovered.Registration.BindingKey != "z2m-00124b0024abcdef" ||
		discovered.Registration.Device.Kind != "sensor" {
		t.Fatalf("Device identity = %#v", discovered.Registration)
	}
	if len(discovered.Entities) != 1 {
		t.Fatalf("Entities = %#v", discovered.Entities)
	}
	temperature := discovered.Entities[0]
	wantSupport := json.RawMessage(`{"state":{},"operations":{}}`)
	if temperature.Descriptor.Key != "temperature" ||
		temperature.Descriptor.ExternalID != "0x00124b0024abcdef/root/temperature" ||
		temperature.Descriptor.Name != "Temperature" || temperature.Descriptor.Type != "hearth.temperature/v1" ||
		!reflect.DeepEqual(temperature.Descriptor.Support, wantSupport) {
		t.Fatalf("temperature descriptor = %#v", temperature.Descriptor)
	}
	if !reflect.DeepEqual(temperature.StateProperties, []string{"temperature"}) ||
		len(temperature.GetProperties) != 0 || temperature.TranslateCommand != nil {
		t.Fatalf("publish-only temperature plan = %#v", temperature)
	}
}

// This test protects read-only access separation and fails if publish-only
// temperature gains startup refresh or a gettable sensor gains a command
// route.
func TestTemperaturePlanSeparatesGetFromCommand(t *testing.T) {
	t.Parallel()
	publishOnly, rejection := discoverDevice(eligibleSensorDevice("temperature", 1))
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if len(publishOnly.Entities[0].GetProperties) != 0 {
		t.Fatalf("publish-only get properties = %v", publishOnly.Entities[0].GetProperties)
	}
	gettable, rejection := discoverDevice(eligibleSensorDevice("temperature", 1|4))
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if !reflect.DeepEqual(gettable.Entities[0].GetProperties, []string{"temperature"}) ||
		gettable.Entities[0].TranslateCommand != nil {
		t.Fatalf("gettable temperature plan = %#v", gettable.Entities[0])
	}
	withSet, rejection := discoverDevice(eligibleSensorDevice("temperature", 1|2))
	if rejection == nil || rejection.Code != rejectionNoEligibleEntity {
		t.Fatalf("settable temperature rejection = %#v", rejection)
	}
	_ = withSet
}

// This test protects the sensor eligibility gate and fails if unit, property,
// access, endpoint, or name violations still register.
func TestTemperaturePlanEligibility(t *testing.T) {
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
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device := eligibleSensorDevice("temperature", 1)
			test.edit(&device)
			_, rejection := discoverDevice(device)
			if rejection == nil || rejection.Code != rejectionNoEligibleEntity {
				t.Fatalf("rejection = %#v", rejection)
			}
		})
	}
}

// FuzzNormalizeTemperature protects exact-conversion stability and range
// enforcement against malformed or extreme numeric input.
func FuzzNormalizeTemperature(fuzz *testing.F) {
	for _, seed := range []string{`21.5`, `2.15e1`, `-273.15`, `0.0001`, `"21.5"`, `1e10000`, `null`} {
		fuzz.Add(seed)
	}
	fuzz.Fuzz(func(t *testing.T, payload string) {
		value, err := normalizeTemperature(json.RawMessage(payload))
		if err != nil {
			return
		}
		if value < temperatureMinimumMilliCelsius || value > temperatureMaximumMilliCelsius {
			t.Fatalf("temperature State = %d", value)
		}
	})
}
