package esphome //nolint:testpackage // Exercises the planner and canonical/native translation seams.

import (
	"encoding/json"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	sdkthermostatv1 "github.com/mholtzscher/hearth/sdk/adapter/thermostatv1"
)

func rheemClimateInfo() *nativeapi.ClimateInfo {
	return &nativeapi.ClimateInfo{
		Modes:           []string{"off", "heat_cool", "cool", "heat", "fan_only"},
		CustomFanModes:  []string{"Automatic", "Speed 1 (Low)"},
		TemperatureUnit: "Cel", CurrentTemperature: true, TwoPointTarget: true,
		CurrentHumidity: true, TargetHumidity: true, Activity: true,
		MinimumTemperature: 10, MaximumTemperature: 32.22222, TargetTemperatureStep: 0.5,
		MinimumHumidity: 10, MaximumHumidity: 80,
	}
}

func plannedThermostatRoute(t *testing.T, info *nativeapi.ClimateInfo) entityRoute {
	t.Helper()
	plan, err := planDevice(MAC{2, 0, 0, 0, 0, 1}, nativeapi.DeviceInfo{Name: "Rheem"},
		[]nativeapi.EntityInfo{{Type: nativeapi.EntityClimate, Key: 7, Name: "HVAC", Climate: info}}, Exposure{})
	require.NoError(t, err)
	require.Len(t, plan.Routes, 1)
	require.Equal(t, "thermostat", plan.Registration.Device.Kind)
	return plan.Routes[0]
}

func TestThermostatPlanKeepsSiblingIdentityAndAdvertisesExactCapabilities(t *testing.T) {
	t.Parallel()
	mac := MAC{2, 0, 0, 0, 0, 1}
	siblings := []nativeapi.EntityInfo{
		{Type: nativeapi.EntitySensor, Name: "Humidity", DeviceClass: "humidity", Unit: "%"},
		{Type: nativeapi.EntitySwitch, Name: "Follow Schedule"},
	}
	before, err := planDevice(mac, nativeapi.DeviceInfo{Name: "Rheem"}, siblings, Exposure{})
	require.NoError(t, err)
	entities := append(slices.Clone(siblings), nativeapi.EntityInfo{
		Type: nativeapi.EntityClimate, Key: 7, Name: "HVAC", Climate: rheemClimateInfo(),
	})
	after, err := planDevice(mac, nativeapi.DeviceInfo{Name: "Rheem"}, entities, Exposure{})
	require.NoError(t, err)
	require.Equal(t, "thermostat", after.Registration.Device.Kind)
	for _, sibling := range before.Registration.Entities {
		require.Equal(t, sibling, descriptorByExternalID(t, after, sibling.ExternalID))
	}
	climate := descriptorByExternalID(t, after, mac.String()+"/0/climate/HVAC")
	require.Equal(t, "hearth.thermostat/v1", climate.Type)
	require.JSONEq(t, `{"state":{
		"modes":["off","heat_cool","cool","heat","fan_only"],"fan_modes":["Automatic","Speed 1 (Low)"],
		"temperature_minimum":10000,"temperature_maximum":32222,"humidity_minimum":10,"humidity_maximum":80
	},"operations":{"set_mode":{},"set_range":{},"set_fan_mode":{},"set_humidity":{}}}`, string(climate.Support))
}

func TestThermostatPlanningExposureAndMalformedCapabilities(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		change   func(*nativeapi.EntityInfo)
		exposure Exposure
		count    int
	}{
		{"eligible", func(*nativeapi.EntityInfo) {}, Exposure{}, 1},
		{"sub-device", func(entity *nativeapi.EntityInfo) { entity.DeviceID = 1 }, Exposure{}, 0},
		{"diagnostic", func(entity *nativeapi.EntityInfo) { entity.Category = nativeapi.CategoryDiagnostic }, Exposure{}, 0},
		{"disabled-default", func(entity *nativeapi.EntityInfo) { entity.DisabledByDefault = true }, Exposure{}, 0},
		{"included", func(entity *nativeapi.EntityInfo) { entity.Category = nativeapi.CategoryConfig },
			Exposure{Include: map[EntityRef]struct{}{{Type: nativeapi.EntityClimate, Name: "HVAC"}: {}}}, 1},
		{"excluded", func(*nativeapi.EntityInfo) {},
			Exposure{Exclude: map[EntityRef]struct{}{{Type: nativeapi.EntityClimate, Name: "HVAC"}: {}}}, 0},
		{"unknown-unit", func(entity *nativeapi.EntityInfo) { entity.Climate.TemperatureUnit = "unknown" }, Exposure{}, 0},
		{"unknown-mode", func(entity *nativeapi.EntityInfo) { entity.Climate.Modes = []string{"unknown"} }, Exposure{}, 0},
		{"missing-info", func(entity *nativeapi.EntityInfo) { entity.Climate = nil }, Exposure{}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			entity := nativeapi.EntityInfo{Type: nativeapi.EntityClimate, Name: "HVAC", Climate: rheemClimateInfo()}
			test.change(&entity)
			plan, err := planDevice(MAC{2, 0, 0, 0, 0, 1}, nativeapi.DeviceInfo{},
				[]nativeapi.EntityInfo{entity}, test.exposure)
			require.NoError(t, err)
			require.Len(t, plan.Routes, test.count)
		})
	}
	for _, name := range []string{"climate/HVAC", "number/Target", "select/Fan Duty"} {
		_, err := ParseEntityRef(name)
		require.NoError(t, err)
	}
}

func TestThermostatBadControlMetadataPreservesOtherControlsAndMonitoring(t *testing.T) {
	t.Parallel()
	info := rheemClimateInfo()
	info.MinimumTemperature, info.MaximumTemperature = math.NaN(), 0
	info.MinimumHumidity, info.MaximumHumidity = 80, 10
	info.StandardFanModes = []string{"Automatic"} // Collides with a custom wire identity.
	route := plannedThermostatRoute(t, info)
	require.Nil(t, route.Thermostat.Operations.SetRange)
	require.Nil(t, route.Thermostat.Operations.SetTarget)
	require.Nil(t, route.Thermostat.Operations.SetHumidity)
	require.Nil(t, route.Thermostat.Operations.SetFanMode)
	require.NotNil(t, route.Thermostat.Operations.SetMode)
	require.Empty(t, route.Thermostat.State.FanModes)
	require.Equal(t, int64(minimumMilliTemperature), route.Thermostat.State.TemperatureMinimum)
	observation, ok, err := project(route, "ent_climate", nativeapi.StateUpdate{
		Type: nativeapi.EntityClimate, Climate: &nativeapi.ClimateState{
			Mode: "heat", CurrentTemperature: new(20.0), TargetTemperatureLow: new(18.0),
			CurrentHumidity: new(50.0), CustomFanMode: new("Automatic"),
		},
	}, projectionTime())
	require.NoError(t, err)
	require.True(t, ok)
	require.JSONEq(
		t,
		`{"mode":"heat","current_temperature":20000,"target_temperature_low":18000,"current_humidity":50}`,
		string(observation.Value),
	)
}

func TestThermostatProjectionUsesOneSnapshotAndNativeUnits(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		unit  string
		value float64
		milli int64
	}{
		{"Cel", -1.5, -1500}, {"[degF]", 68, 20000}, {"K", 273.15, 0},
	} {
		t.Run(test.unit, func(t *testing.T) {
			t.Parallel()
			info := rheemClimateInfo()
			info.TemperatureUnit = test.unit
			route := plannedThermostatRoute(t, info)
			observation, ok, err := project(route, "ent_climate", nativeapi.StateUpdate{
				Type: nativeapi.EntityClimate, Climate: &nativeapi.ClimateState{
					Mode: "heat", CurrentTemperature: &test.value, TargetTemperatureLow: &test.value,
					CurrentHumidity: new(0.0), TargetHumidity: new(101.0), Activity: new("idle"),
				},
			}, projectionTime())
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, projectionTime().Format(time.RFC3339Nano), observation.AdapterReceivedAt)
			var state sdkthermostatv1.State
			require.NoError(t, json.Unmarshal(observation.Value, &state))
			require.Equal(t, &test.milli, state.CurrentTemperature)
			require.Equal(t, &test.milli, state.TargetTemperatureLow)
			require.Nil(t, state.TargetTemperatureHigh)
			require.Nil(t, state.TargetTemperature)
			require.Equal(t, new(0.0), state.CurrentHumidity)
			require.Nil(t, state.TargetHumidity)
			require.Equal(t, new("idle"), state.Activity)
		})
	}
	route := plannedThermostatRoute(t, rheemClimateInfo())
	for _, update := range []nativeapi.StateUpdate{
		{Type: nativeapi.EntityClimate, Missing: true},
		{Type: nativeapi.EntityClimate},
		{Type: nativeapi.EntityClimate, Climate: &nativeapi.ClimateState{Mode: "unrecognized"}},
	} {
		_, ok, _ := project(route, "ent_climate", update, projectionTime())
		require.False(t, ok)
	}
}

func TestThermostatSingleTargetAndUnadvertisedFields(t *testing.T) {
	t.Parallel()
	info := rheemClimateInfo()
	info.TwoPointTarget, info.CurrentTemperature, info.CurrentHumidity, info.TargetHumidity, info.Activity = false, false, false, false, false
	info.CustomFanModes = nil
	route := plannedThermostatRoute(t, info)
	require.NotNil(t, route.Thermostat.Operations.SetTarget)
	require.Nil(t, route.Thermostat.Operations.SetRange)
	observation, ok, err := project(route, "ent_climate", nativeapi.StateUpdate{
		Type: nativeapi.EntityClimate, Climate: &nativeapi.ClimateState{
			Mode: "off", CurrentTemperature: new(20.0), TargetTemperature: new(0.0),
			TargetTemperatureLow: new(18.0), CurrentHumidity: new(50.0), TargetHumidity: new(55.0),
			Activity: new("off"), StandardFanMode: new("on"),
		},
	}, projectionTime())
	require.NoError(t, err)
	require.True(t, ok)
	require.JSONEq(t, `{"mode":"off","target_temperature":0}`, string(observation.Value))
}

func TestThermostatCommandsPreserveUnrelatedSettingsAndMatchTheirOwnFields(t *testing.T) {
	t.Parallel()
	pair := plannedThermostatRoute(t, rheemClimateInfo())
	singleInfo := rheemClimateInfo()
	singleInfo.TwoPointTarget = false
	single := plannedThermostatRoute(t, singleInfo)
	for _, test := range []struct {
		operation, parameters, matched, unmatched string
		route                                     entityRoute
		native                                    nativeapi.ClimateCommand
	}{
		{"set_mode", `{"mode":"off"}`, `{"mode":"off","activity":"heating"}`, `{"mode":"heat"}`, pair,
			nativeapi.ClimateCommand{Mode: new("off")}},
		{"set_target", `{"temperature":20000}`, `{"mode":"off","target_temperature":20000}`,
			`{"mode":"heat","current_temperature":20000}`, single, nativeapi.ClimateCommand{TargetTemperature: new(20.0)}},
		{"set_range", `{"low":18333,"high":22000}`, `{"mode":"heat","target_temperature_low":18333,"target_temperature_high":22000}`,
			`{"mode":"heat","target_temperature_low":18333}`, pair,
			nativeapi.ClimateCommand{TargetTemperatureLow: new(18.333), TargetTemperatureHigh: new(22.0)}},
		{"set_fan_mode", `{"fan_mode":"Automatic"}`, `{"mode":"off","fan_mode":"Automatic"}`,
			`{"mode":"off","fan_mode":"Speed 1 (Low)"}`, pair, nativeapi.ClimateCommand{CustomFanMode: new("Automatic")}},
		{"set_humidity", `{"humidity":55.5}`, `{"mode":"off","target_humidity":55.5}`,
			`{"mode":"off","current_humidity":55.5}`, pair, nativeapi.ClimateCommand{TargetHumidity: new(55.5)}},
	} {
		t.Run(test.operation, func(t *testing.T) {
			t.Parallel()
			parameters, native, err := prepareThermostatCommand(boundRoute{entityRoute: test.route},
				test.operation, []byte(test.parameters))
			require.NoError(t, err)
			test.native.Key = 7
			require.Equal(t, test.native, native)
			require.True(t, thermostatSatisfied(test.operation, parameters, []byte(test.matched)))
			require.False(t, thermostatSatisfied(test.operation, parameters, []byte(test.unmatched)))
		})
	}
}

func TestThermostatRejectsInvalidAndUnadvertisedCommands(t *testing.T) {
	t.Parallel()
	route := boundRoute{entityRoute: plannedThermostatRoute(t, rheemClimateInfo())}
	for _, test := range []struct{ operation, parameters string }{
		{"set", `{"value":true}`}, {"set_mode", `{"mode":"dry"}`}, {"set_mode", `{}`},
		{"set_target", `{"temperature":20000}`}, {"set_range", `{"low":22000,"high":18000}`},
		{"set_range", `{"low":9999,"high":22000}`}, {"set_range", `{"low":18000}`},
		{"set_fan_mode", `{"fan_mode":"auto"}`}, {"set_humidity", `{"humidity":80.1}`},
		{"set_humidity", `{"humidity":33.333333333333}`},
	} {
		_, _, err := prepareThermostatCommand(route, test.operation, []byte(test.parameters))
		require.Error(t, err, "%s %s", test.operation, test.parameters)
	}
	route.Thermostat.Operations = sdkthermostatv1.OperationSupport{}
	_, _, err := prepareThermostatCommand(route, "set_mode", []byte(`{"mode":"off"}`))
	require.Error(t, err)
}
