package esphome //nolint:testpackage // Reproduces upstream fan ambiguity and write-bound conversion defects.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
)

func TestThermostatRejectsReservedCustomFanNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Low", "AUTO", "quiet", "On"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			info := rheemClimateInfo()
			info.CustomFanModes = []string{"Automatic", name}
			route := plannedThermostatRoute(t, info)
			require.Empty(t, route.Thermostat.State.FanModes)
			require.Nil(t, route.Thermostat.Operations.SetFanMode)
			_, _, err := prepareThermostatCommand(boundRoute{entityRoute: route}, "set_fan_mode",
				[]byte(`{"fan_mode":"`+name+`"}`))
			require.Error(t, err)
		})
	}
}

func TestThermostatDoesNotUseAmbiguousOnFanAsEvidence(t *testing.T) {
	t.Parallel()
	info := rheemClimateInfo()
	info.CustomFanModes = nil
	info.StandardFanModes = []string{"on", "auto"}
	route := plannedThermostatRoute(t, info)
	require.Equal(t, []string{"auto"}, []string(route.Thermostat.State.FanModes))
	observation, ok, err := project(route, "ent_climate", nativeapi.StateUpdate{
		Type: nativeapi.EntityClimate, Climate: &nativeapi.ClimateState{Mode: "off", StandardFanMode: new("on")},
	}, projectionTime())
	require.NoError(t, err)
	require.True(t, ok)
	require.JSONEq(t, `{"mode":"off"}`, string(observation.Value))
	require.False(t, thermostatSatisfied("set_fan_mode", []byte(`{"fan_mode":"on"}`), observation.Value))
	_, _, err = prepareThermostatCommand(boundRoute{entityRoute: route}, "set_fan_mode", []byte(`{"fan_mode":"on"}`))
	require.Error(t, err)
}

func TestThermostatWriteBoundsRoundInward(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		unit             string
		minimum, maximum float64
	}{
		{"Cel", 10.0004, 20.0006},
		{"[degF]", 50.00072, 68.00108},
		{"K", 283.1504, 293.1506},
	} {
		t.Run(test.unit, func(t *testing.T) {
			t.Parallel()
			info := rheemClimateInfo()
			info.TemperatureUnit, info.MinimumTemperature, info.MaximumTemperature = test.unit, test.minimum, test.maximum
			route := plannedThermostatRoute(t, info)
			require.Equal(t, int64(10001), route.Thermostat.State.TemperatureMinimum)
			require.Equal(t, int64(20000), route.Thermostat.State.TemperatureMaximum)
			for _, parameters := range []string{`{"low":10000,"high":20000}`, `{"low":10001,"high":20001}`} {
				_, _, err := prepareThermostatCommand(boundRoute{entityRoute: route}, "set_range", []byte(parameters))
				require.Error(t, err)
			}
		})
	}
	info := rheemClimateInfo()
	info.MinimumTemperature, info.MaximumTemperature = 10.0002, 10.0004
	route := plannedThermostatRoute(t, info)
	require.Nil(t, route.Thermostat.Operations.SetRange)
	require.NotNil(t, route.Thermostat.Operations.SetMode)
}

func TestThermostatWriteBoundsKeepExactlyRepresentableDecimalEndpoints(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		unit  string
		bound float64
		milli int64
	}{
		{"[degF]", 32.9, 500}, {"Cel", 20.001, 20001}, {"K", 273.15, 0},
	} {
		t.Run(test.unit, func(t *testing.T) {
			t.Parallel()
			info := rheemClimateInfo()
			info.TemperatureUnit, info.MinimumTemperature, info.MaximumTemperature = test.unit, test.bound, test.bound
			route := plannedThermostatRoute(t, info)
			require.Equal(t, test.milli, route.Thermostat.State.TemperatureMinimum)
			require.Equal(t, test.milli, route.Thermostat.State.TemperatureMaximum)
			require.NotNil(t, route.Thermostat.Operations.SetRange)
		})
	}
}
