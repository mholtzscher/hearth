package homekit

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestMappingConversions(t *testing.T) {
	t.Parallel()
	if got := celsius(21500); got != 21.5 || milliCelsius(got) != 21500 {
		t.Fatalf("temperature conversion got %v", got)
	}
	for i, want := range []float64{25, 50, 75, 100} {
		if got := speedPercent(i, 4); got != want || speedIndex(got, 4) != i {
			t.Errorf("speed index %d: got %v", i, got)
		}
	}
	for _, tc := range []struct {
		pm   float64
		want int
	}{
		{0, 1}, {12, 1}, {12.1, 2}, {35, 2}, {35.1, 3}, {55, 3},
		{55.1, 4}, {150, 4}, {150.1, 5},
	} {
		if got := airQuality(tc.pm); got != tc.want {
			t.Errorf("air quality %v: got %d, want %d", tc.pm, got, tc.want)
		}
	}
}

func TestMappingWriteValidationAndIntents(t *testing.T) {
	t.Parallel()
	hue := integerWriter("color", "hue", "hsv", "hue", 0, 360)
	got, code := hue(float64(360))
	want := writeIntent{
		EntityID:   "color",
		Operation:  "set",
		Parameters: map[string]any{"hue": int64(359)},
		Group:      "hsv",
		Field:      "hue",
	}
	if code != 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("hue: %#v, %d", got, code)
	}
	for _, bad := range []any{-1.0, 361.0, 1.5, "1", math.NaN(), math.Inf(1)} {
		if _, status := hue(bad); status != invalidValue {
			t.Errorf("hue %v accepted: %d", bad, status)
		}
	}
	rangeWrite := temperatureWriter("therm", "set_range", "low", "low", 10, 30)
	got, code = rangeWrite(21.5)
	want = writeIntent{
		EntityID:   "therm",
		Operation:  "set_range",
		Parameters: map[string]any{"low": int64(21500)},
		Group:      "range",
		Field:      "low",
	}
	if code != 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("range: %#v, %d", got, code)
	}
	if _, code = boolWriter("on", "on")(float64(1)); code != invalidValue {
		t.Fatal("numeric boolean accepted")
	}
	if _, code = binaryIntWriter("active")(1.5); code != invalidValue {
		t.Fatal("fractional enum accepted")
	}
}

func TestMappingMissingStateAndInversion(t *testing.T) {
	t.Parallel()
	if _, ok := readBool(Entity{}); ok {
		t.Fatal("missing state accepted")
	}
	if _, ok := readNumber(Entity{State: &EntityState{Value: json.RawMessage("null")}}); ok {
		t.Fatal("null state accepted")
	}
	entity := Entity{State: &EntityState{Value: json.RawMessage("true")}}
	for _, tc := range []struct {
		inverted, boolean bool
		want              any
	}{
		{false, false, 1}, {true, false, 0}, {false, true, true}, {true, true, false},
	} {
		got, ok := binaryReader(tc.inverted, tc.boolean)(entity)
		if !ok || got != tc.want {
			t.Errorf("inverted %v boolean %v: %v, %v", tc.inverted, tc.boolean, got, ok)
		}
	}
}

func TestThermostatModeAndAbsentFields(t *testing.T) {
	t.Parallel()
	for mode, want := range map[string]int{"off": 0, "heat": 1, "cool": 2, "auto": 3, "heat_cool": 3} {
		got, ok := targetMode(mode)
		if !ok || got != want {
			t.Errorf("mode %s: %d, %v", mode, got, ok)
		}
	}
	if _, ok := targetMode("fan_only"); ok {
		t.Fatal("fan-only became selectable")
	}
	e := Entity{State: &EntityState{Value: json.RawMessage(`{"mode":"heat","current_temperature":21500}`)}}
	if got, ok := thermostatTemperature("current")(e); !ok || got != 21.5 {
		t.Fatalf("temperature: %v, %v", got, ok)
	}
	if _, ok := thermostatTemperature("low")(e); ok {
		t.Fatal("missing threshold accepted")
	}
	if _, ok := thermostatHumidity(false)(e); ok {
		t.Fatal("missing humidity accepted")
	}
}
