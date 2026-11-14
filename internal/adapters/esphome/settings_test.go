package esphome //nolint:testpackage // Exercises the setting planner, projection, and native request boundary.

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
)

func TestSettingsUseExistingContractsAndPreserveAdvertisedUnits(t *testing.T) {
	t.Parallel()
	plan, err := planDevice(MAC{2, 0, 0, 0, 0, 1}, nativeapi.DeviceInfo{Name: "Settings"}, []nativeapi.EntityInfo{
		{Type: nativeapi.EntityNumber, Key: 1, Name: "Humidification Target Percentage",
			Number: &nativeapi.NumberInfo{Minimum: 10, Maximum: 80, Step: 1}},
		{Type: nativeapi.EntityNumber, Key: 2, Name: "Offset", Unit: "opaque-unit",
			Number: &nativeapi.NumberInfo{Minimum: -9.5, Maximum: 9.5, Step: 0.5}},
		{Type: nativeapi.EntitySelect, Key: 3, Name: "Fan Duty", Options: []string{"Continuous", "Timed, 10 min"}},
	}, Exposure{})
	require.NoError(t, err)
	require.Len(t, plan.Routes, 3)
	require.Equal(t, "relay", plan.Registration.Device.Kind)
	require.JSONEq(t, `{"state":{"minimum":10,"maximum":80,"choices":[]},"operations":{"set":{}}}`,
		string(plan.Registration.Entities[0].Support))
	require.JSONEq(
		t,
		`{"state":{"minimum":-9.5,"maximum":9.5,"choices":[],"unit":"opaque-unit"},"operations":{"set":{}}}`,
		string(plan.Registration.Entities[1].Support),
	)
	require.JSONEq(t, `{"state":{"choices":["Continuous","Timed, 10 min"]},"operations":{"set":{}}}`,
		string(plan.Registration.Entities[2].Support))
	for index, test := range []struct {
		update     nativeapi.StateUpdate
		parameters string
		native     nativeapi.Command
		value      string
	}{
		{nativeapi.StateUpdate{Type: nativeapi.EntityNumber, Number: 55}, `{"mode":"value","value":55}`,
			nativeapi.NumberCommand{Key: 1, Value: 55}, `{"mode":"value","value":55}`},
		{nativeapi.StateUpdate{Type: nativeapi.EntityNumber, Number: 0.1}, `{"mode":"value","value":0.1}`,
			nativeapi.NumberCommand{Key: 2, Value: 0.1}, `{"mode":"value","value":0.1}`},
		{nativeapi.StateUpdate{Type: nativeapi.EntitySelect, Text: "Continuous"}, `{"value":"Continuous"}`,
			nativeapi.SelectCommand{Key: 3, Value: "Continuous"}, `"Continuous"`},
	} {
		route := plan.Routes[index]
		observation, ok, projectErr := project(route, "ent_setting", test.update, projectionTime())
		require.NoError(t, projectErr)
		require.True(t, ok)
		require.JSONEq(t, test.value, string(observation.Value))
		target, native, commandErr := prepareSet(boundRoute{entityRoute: route}, []byte(test.parameters))
		require.NoError(t, commandErr)
		require.Equal(t, test.native, native)
		require.True(t, stateMatches(route.Kind, observation.Value, target))
	}
}

func TestSettingsRejectMalformedCapabilitiesAndHonorExposure(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		entity nativeapi.EntityInfo
	}{
		{"missing-number", nativeapi.EntityInfo{Type: nativeapi.EntityNumber}},
		{"nonfinite", nativeapi.EntityInfo{Type: nativeapi.EntityNumber,
			Number: &nativeapi.NumberInfo{Minimum: math.NaN(), Maximum: 100}}},
		{"inverted", nativeapi.EntityInfo{Type: nativeapi.EntityNumber,
			Number: &nativeapi.NumberInfo{Minimum: 10, Maximum: 0}}},
		{"empty-choices", nativeapi.EntityInfo{Type: nativeapi.EntitySelect, Options: []string{}}},
		{"empty-choice", nativeapi.EntityInfo{Type: nativeapi.EntitySelect, Options: []string{""}}},
		{"duplicate-choice", nativeapi.EntityInfo{Type: nativeapi.EntitySelect, Options: []string{"A", "A"}}},
		{"config", nativeapi.EntityInfo{Type: nativeapi.EntitySelect, Category: nativeapi.CategoryConfig, Options: []string{"A"}}},
		{"sub-device", nativeapi.EntityInfo{Type: nativeapi.EntitySelect, DeviceID: 1, Options: []string{"A"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, err := planDevice(
				MAC{2, 0, 0, 0, 0, 1},
				nativeapi.DeviceInfo{},
				[]nativeapi.EntityInfo{test.entity},
				Exposure{},
			)
			require.NoError(t, err)
			require.Empty(t, plan.Routes)
		})
	}
}

func TestSettingsDoNotRoundOrFabricateState(t *testing.T) {
	t.Parallel()
	number, err := settingRoute(upstreamRef{Type: nativeapi.EntityNumber, Key: 1}, nativeapi.EntityInfo{
		Type: nativeapi.EntityNumber, Number: &nativeapi.NumberInfo{Minimum: -10, Maximum: 100, Step: 0.5},
	})
	require.NoError(t, err)
	selection, err := settingRoute(upstreamRef{Type: nativeapi.EntitySelect, Key: 2}, nativeapi.EntityInfo{
		Type: nativeapi.EntitySelect, Options: []string{"A", "B"},
	})
	require.NoError(t, err)
	for _, parameters := range []string{
		`{"mode":"value","value":100.1}`, `{"mode":"choice","choice":"A"}`,
		`{"mode":"value","value":0.123456789}`, `{"value":10}`,
	} {
		_, _, err = prepareSet(boundRoute{entityRoute: number}, []byte(parameters))
		require.Error(t, err)
	}
	// Step is not SET validation in ESPHome; 0.1 is preserved, not rounded to 0 or 0.5.
	_, native, err := prepareSet(boundRoute{entityRoute: number}, []byte(`{"mode":"value","value":0.1}`))
	require.NoError(t, err)
	require.Equal(t, nativeapi.NumberCommand{Key: 1, Value: 0.1}, native)
	_, _, err = prepareSet(boundRoute{entityRoute: selection}, []byte(`{"value":"C"}`))
	require.Error(t, err)
	for _, update := range []nativeapi.StateUpdate{
		{Type: nativeapi.EntityNumber, Missing: true},
		{Type: nativeapi.EntityNumber, Number: math.NaN()},
		{Type: nativeapi.EntityNumber, Number: math.Inf(1)},
	} {
		_, ok, projectErr := project(number, "ent_setting", update, projectionTime())
		require.NoError(t, projectErr)
		require.False(t, ok)
	}
	_, ok, err := project(
		selection,
		"ent_setting",
		nativeapi.StateUpdate{Type: nativeapi.EntitySelect, Missing: true},
		projectionTime(),
	)
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = project(
		selection,
		"ent_setting",
		nativeapi.StateUpdate{Type: nativeapi.EntitySelect, Text: "C"},
		projectionTime(),
	)
	require.Error(t, err)
	require.False(t, ok)
}
