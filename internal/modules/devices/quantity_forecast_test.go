package devices_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/household"
)

func TestForecastPresentationUsesNeutralFieldsAndSharedPolicy(t *testing.T) {
	t.Parallel()
	canonical := `{"periods":[{"starts_at":"2026-11-10T12:00:00Z","condition":"clear-day","summary":"Source prediction","temperature_c":1.6005e1,"apparent_temperature_c":-40,"precipitation_probability_percent":50.125,"precipitation_rate_mmh":2.54,"humidity_percent":70.25,"pressure_hpa":1013.2,"wind_speed_mps":3.4,"wind_gust_mps":5,"wind_direction_degrees":180,"cloud_cover_percent":10,"uv_index":2},{"starts_at":"2026-11-10T13:00:00Z"}]}`
	for _, test := range []struct {
		system                             household.UnitSystem
		temperature, rate, pressure, speed float64
		unit                               string
	}{
		{household.UnitSystemUS, 60.809, 0.1, 29.919866023257644, 7.605583392984967, "[degF]"},
		{household.UnitSystemMetric, 16.005, 2.54, 1013.2, 3.4, "Cel"},
	} {
		t.Run(string(test.system), func(t *testing.T) {
			t.Parallel()
			view := quantityView(devices.EntityTypeHourlyforecastV1, `{}`, canonical)
			presenter := devices.NewPresenter(test.system, devices.TableConverter{})
			result, quantities, err := presenter.Entity(view, "")
			require.NoError(t, err)
			var state struct {
				Periods []map[string]json.RawMessage `json:"periods"`
			}
			require.NoError(t, json.Unmarshal(result.State.Value, &state))
			for field, want := range map[string]float64{
				"temperature": test.temperature, "apparent_temperature": -40,
				"precipitation_rate": test.rate, "pressure": test.pressure,
				"wind_speed": test.speed, "precipitation_probability": 50.125,
				"humidity": 70.25, "wind_direction": 180, "cloud_cover": 10, "uv_index": 2,
			} {
				var value float64
				require.NoError(t, json.Unmarshal(state.Periods[0][field], &value))
				require.InDelta(t, want, value, 1e-10, field)
				require.Contains(t, quantities, "/periods/0/"+field)
			}
			require.Len(t, quantities, 11)
			require.Len(t, state.Periods[1], 1)
			require.NotContains(t, state.Periods[0], "temperature_c")
			require.JSONEq(t, `"Source prediction"`, string(state.Periods[0]["summary"]))
			require.JSONEq(t, `"clear-day"`, string(state.Periods[0]["condition"]))
			require.Equal(t, test.unit, quantities["/periods/0/temperature"].Unit)
			require.Zero(t, quantities["/periods/0/temperature"].FractionDigits)
			require.Nil(t, quantities["/periods/0/temperature"].Range)
			require.Equal(t, canonical, string(view.State.Value))
			require.Equal(t, view.State.ObservationID, result.State.ObservationID)

			source, metadata, err := presenter.Entity(view, devices.UnitsCanonical)
			require.NoError(t, err)
			require.Equal(t, canonical, string(source.State.Value))
			require.Equal(t, "Cel", metadata["/periods/0/temperature_c"].Unit)
			require.NotContains(t, metadata, "/periods/0/temperature")
		})
	}
}

func TestForecastDailyBoundsAndMissingValues(t *testing.T) {
	t.Parallel()
	presenter := devices.NewPresenter(household.UnitSystemUS, devices.TableConverter{})
	view := quantityView(
		devices.EntityTypeDailyforecastV1,
		`{}`,
		`{"periods":[{"starts_at":"2026-11-10T00:00:00Z","date":"2026-11-10","temperature_min_c":0,"temperature_max_c":25,"precipitation_probability_percent":0},{"starts_at":"2026-11-11T00:00:00Z","date":"2026-11-11","temperature_max_c":10}]}`,
	)
	result, metadata, err := presenter.Entity(view, "")
	require.NoError(t, err)
	var state struct {
		Periods []map[string]any `json:"periods"`
	}
	require.NoError(t, json.Unmarshal(result.State.Value, &state))
	require.InDelta(t, 32, state.Periods[0]["temperature_min"], 1e-10)
	require.InDelta(t, 77, state.Periods[0]["temperature_max"], 1e-10)
	require.InDelta(t, 0, state.Periods[0]["precipitation_probability"], 1e-10)
	require.Equal(t, "2026-11-10", state.Periods[0]["date"])
	require.NotContains(t, state.Periods[1], "temperature_min")
	require.NotContains(t, metadata, "/periods/1/temperature_min")
	require.Len(t, metadata, 4)
	view.State = nil
	result, metadata, err = presenter.Entity(view, "")
	require.NoError(t, err)
	require.Nil(t, result.State)
	require.Empty(t, metadata)
}
