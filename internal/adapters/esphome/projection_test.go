package esphome //nolint:testpackage // Projection is an unexported translation.

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
)

func projectionTime() time.Time { return time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC) }

func sensorRoute(t *testing.T, deviceClass, unit string) entityRoute {
	t.Helper()
	mapping, ok := catalogSensor(deviceClass, unit)
	if !ok {
		t.Fatalf("no catalog row for %s/%s", deviceClass, unit)
	}
	return entityRoute{Kind: mapping.kind, Conversion: mapping.conversion, Numeric: numericSupport(mapping)}
}

func TestProjectConvertsSensorUnits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		deviceClass, unit string
		value             float64
		want              string
	}{
		{"temperature", "°C", -23.9, "-23900"},
		{"temperature", "°F", 32, "0"},
		{"temperature", "°F", 212, "100000"},
		{"humidity", "%", 44.36, "44.36"},
		{"pressure", "hPa", 985.4, "985.4"},
		{"pressure", "Pa", 101325, "1013.25"},
		{"atmospheric_pressure", "kPa", 101.3, "1013"},
		{"carbon_dioxide", "ppm", 1572, "1572"},
		{"", "ppm", 4.998, "4.998"},
	}
	for _, test := range cases {
		route := sensorRoute(t, test.deviceClass, test.unit)
		observation, ok, err := project(
			route,
			"ent_1",
			nativeapi.StateUpdate{Type: nativeapi.EntitySensor, Number: test.value},
			projectionTime(),
		)
		if err != nil || !ok {
			t.Errorf("%s %v%s: ok=%v err=%v", test.deviceClass, test.value, test.unit, ok, err)
			continue
		}
		if string(observation.Value) != test.want {
			t.Errorf("%s %v%s = %s, want %s", test.deviceClass, test.value, test.unit, observation.Value, test.want)
		}
		if observation.EntityID != "ent_1" || observation.SourceUpdatedAt != nil {
			t.Errorf("observation = %+v", observation)
		}
	}
}

func TestProjectPublishesNothingWithoutAValue(t *testing.T) {
	t.Parallel()
	humidity := sensorRoute(t, "humidity", "%")
	cases := map[string]struct {
		update  nativeapi.StateUpdate
		invalid bool
	}{
		"missing":        {update: nativeapi.StateUpdate{Type: nativeapi.EntitySensor, Missing: true}},
		"NaN":            {update: nativeapi.StateUpdate{Type: nativeapi.EntitySensor, Number: math.NaN()}},
		"infinite":       {update: nativeapi.StateUpdate{Type: nativeapi.EntitySensor, Number: math.Inf(1)}},
		"out of range":   {update: nativeapi.StateUpdate{Type: nativeapi.EntitySensor, Number: 101}, invalid: true},
		"binary missing": {update: nativeapi.StateUpdate{Type: nativeapi.EntityBinarySensor, Missing: true}},
	}
	for name, test := range cases {
		route := humidity
		if test.update.Type == nativeapi.EntityBinarySensor {
			route = entityRoute{Kind: routeBinary}
		}
		_, ok, err := project(route, "ent_1", test.update, projectionTime())
		if ok {
			t.Errorf("%s published", name)
		}
		if test.invalid != errors.Is(err, errUnprojectable) {
			t.Errorf("%s error = %v", name, err)
		}
	}
}

func TestProjectLightBrightness(t *testing.T) {
	t.Parallel()
	cases := []struct {
		on         bool
		brightness float64
		want       string
	}{
		{true, 0.5, "50"},
		{true, 1, "100"},
		{true, 0.004, "1"},
		{false, 0.8, "0"},
	}
	for _, test := range cases {
		update := nativeapi.StateUpdate{Type: nativeapi.EntityLight, Bool: test.on, Brightness: test.brightness}
		observation, ok, err := project(entityRoute{Kind: routeLightBrightness}, "ent_1", update, projectionTime())
		if err != nil || !ok || string(observation.Value) != test.want {
			t.Errorf(
				"on=%v brightness=%v: %s %v %v, want %s",
				test.on,
				test.brightness,
				observation.Value,
				ok,
				err,
				test.want,
			)
		}
		power, _, _ := project(entityRoute{Kind: routeLightPower}, "ent_2", update, projectionTime())
		if want := map[bool]string{true: "true", false: "false"}[test.on]; string(power.Value) != want {
			t.Errorf("power = %s, want %s", power.Value, want)
		}
	}
}

func TestStateMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind          routeKind
		value, target string
		want          bool
	}{
		{routeSwitchPower, "true", "true", true},
		{routeSwitchPower, "false", "true", false},
		{routeLightBrightness, "51", "50", true},
		{routeLightBrightness, "49", "50", true},
		{routeLightBrightness, "52", "50", false},
		{routeLightBrightness, "0", "0", true},
	}
	for _, test := range cases {
		if got := stateMatches(test.kind, []byte(test.value), []byte(test.target)); got != test.want {
			t.Errorf("stateMatches(%d, %s, %s) = %v", test.kind, test.value, test.target, got)
		}
	}
}

func TestCatalogFallsBackToGenericForUnknownUnits(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ deviceClass, unit, wantUnit string }{
		{"temperature", "K", "K"},
		{"aqi", "", "index"},
		{"", "", "1"},
		{"signal_strength", "dBm", "dBm"},
	} {
		mapping, ok := catalogSensor(test.deviceClass, test.unit)
		if !ok {
			t.Fatalf("%s/%s rejected", test.deviceClass, test.unit)
		}
		if test.deviceClass == "temperature" && mapping.kind != routeNumeric {
			t.Errorf("temperature in K kind = %d", mapping.kind)
		}
		if mapping.kind == routeNumeric && mapping.unit != test.wantUnit {
			t.Errorf("%s/%s unit = %q, want %q", test.deviceClass, test.unit, mapping.unit, test.wantUnit)
		}
	}
	if _, ok := catalogSensor("", "abcdefghijklmnopqrstuvwxyzabcdefg"); ok {
		t.Error("33-character unit accepted")
	}
}
