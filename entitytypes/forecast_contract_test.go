package entitytypes_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/entitytypes/dailyforecastv1"
	"github.com/mholtzscher/hearth/entitytypes/hourlyforecastv1"
)

func TestForecastContractsUseUnitNeutralMeasurements(t *testing.T) {
	t.Parallel()
	hourly, err := hourlyforecastv1.Compile()
	require.NoError(t, err)
	daily, err := dailyforecastv1.Compile()
	require.NoError(t, err)
	for _, test := range []struct {
		name   string
		decode func(json.RawMessage) (json.RawMessage, error)
		state  string
		legacy string
	}{
		{
			name: "hourly",
			decode: func(raw json.RawMessage) (json.RawMessage, error) {
				_, normalized, decodeErr := hourly.State.Decode(raw)
				return normalized, decodeErr
			},
			state:  `{"periods":[{"starts_at":"2026-11-10T12:00:00Z","temperature":2.5,"apparent_temperature":0,"precipitation_probability":0,"precipitation_rate":1.5,"humidity":50,"pressure":1013.2,"wind_speed":3.4,"wind_gust":5,"wind_direction":180,"cloud_cover":20,"uv_index":1}]}`,
			legacy: `{"periods":[{"starts_at":"2026-11-10T12:00:00Z","temperature_c":2.5}]}`,
		},
		{
			name: "daily",
			decode: func(raw json.RawMessage) (json.RawMessage, error) {
				_, normalized, decodeErr := daily.State.Decode(raw)
				return normalized, decodeErr
			},
			state:  `{"periods":[{"starts_at":"2026-11-10T00:00:00Z","date":"2026-11-10","temperature_min":-1.5,"temperature_max":5,"precipitation_probability":50,"precipitation_rate":1.5,"humidity":50,"pressure":1013.2,"wind_speed":3.4,"wind_gust":5,"wind_direction":180,"cloud_cover":20,"uv_index":1}]}`,
			legacy: `{"periods":[{"starts_at":"2026-11-10T00:00:00Z","date":"2026-11-10","temperature_min_c":-1.5}]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			normalized, decodeErr := test.decode(json.RawMessage(test.state))
			require.NoError(t, decodeErr)
			require.JSONEq(t, test.state, string(normalized))
			_, decodeErr = test.decode(json.RawMessage(test.legacy))
			require.Error(t, decodeErr, "unit-suffixed input must not remain an accepted alias")
		})
	}
}
