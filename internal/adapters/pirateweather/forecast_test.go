package pirateweather //nolint:testpackage // Exercises forecast conversion at the provider boundary.

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	dailytype "github.com/mholtzscher/hearth/entitytypes/dailyforecastv1"
	hourlytype "github.com/mholtzscher/hearth/entitytypes/hourlyforecastv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/dailyforecastv1"
	"github.com/mholtzscher/hearth/sdk/adapter/hourlyforecastv1"
)

func TestForecastConditionVocabulary(t *testing.T) {
	t.Parallel()
	for _, icon := range []string{"clear-day", "clear-night", "partly-cloudy-day", "partly-cloudy-night", "cloudy", "rain", "snow", "sleet", "thunderstorm", "wind", "fog"} {
		if got := forecastCondition(&icon); got == nil || *got != icon {
			t.Errorf("%s: %v", icon, got)
		}
	}
	unknown, empty := "pirate", ""
	for _, icon := range []*string{nil, &unknown, &empty} {
		if got := forecastCondition(icon); got != nil {
			t.Errorf("unknown condition: %v", *got)
		}
	}
}

func TestForecastSDKObservationsUseNeutralKeysAndPreserveZero(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	zero := 0.0
	hourly, err := hourlyforecastv1.NewObservation(hourlyforecastv1.ObservationInput{
		EntityID: "ent_019a68f8-0000-7000-8000-000000000001", AdapterReceivedAt: now,
		State: hourlyforecastv1.State{Periods: hourlytype.StatePeriods{{StartsAt: now.Format(time.RFC3339),
			Temperature: &zero, ApparentTemperature: &zero, Humidity: &zero, PrecipitationProbability: &zero}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	daily, err := dailyforecastv1.NewObservation(dailyforecastv1.ObservationInput{
		EntityID:          "ent_019a68f8-0000-7000-8000-000000000002",
		AdapterReceivedAt: now,
		State: dailyforecastv1.State{
			Periods: dailytype.StatePeriods{{StartsAt: now.Format(time.RFC3339), Date: "2026-11-09",
				TemperatureMin: &zero, TemperatureMax: &zero, Humidity: &zero, PrecipitationProbability: &zero}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		value json.RawMessage
		want  map[string]any
	}{
		{hourly.Value, map[string]any{"starts_at": "2026-11-09T12:00:00Z", "temperature": 0.0,
			"apparent_temperature": 0.0, "humidity": 0.0, "precipitation_probability": 0.0}},
		{daily.Value, map[string]any{"starts_at": "2026-11-09T12:00:00Z", "date": "2026-11-09", "temperature_min": 0.0,
			"temperature_max": 0.0, "humidity": 0.0, "precipitation_probability": 0.0}},
	} {
		var state struct {
			Periods []map[string]any `json:"periods"`
		}
		if unmarshalErr := json.Unmarshal(tc.value, &state); unmarshalErr != nil {
			t.Fatal(unmarshalErr)
		}
		if len(state.Periods) != 1 || !reflect.DeepEqual(state.Periods[0], tc.want) {
			t.Errorf("observation = %s, want period %v", tc.value, tc.want)
		}
	}
}

func TestForecastFunctionResultContracts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	body := fullForecastBody(t, now, 1, 1)
	c := testClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, body), nil
	}))
	c.now = func() time.Time { return now }
	common := map[string]any{"starts_at": "2026-11-09T12:00:00Z", "summary": "Rain expected", "icon": "rain",
		"precipitation_type": "rain", "humidity": 42.0, "pressure": 1013.0, "wind_speed": 3.0,
		"wind_gust": 5.0, "wind_direction": 180.0, "precipitation_rate": 0.5,
		"precipitation_probability": 70.0, "cloud_cover": 30.0, "uv_index": 2.0}
	for i, function := range c.functions() {
		args := `{"hours":1}`
		want := make(map[string]any, len(common)+3)
		maps.Copy(want, common)
		if i == 0 {
			want["temperature"], want["apparent_temperature"] = 12.5, 11.5
		} else {
			args = `{"days":1}`
			want["date"], want["temperature_min"], want["temperature_max"] = "2026-11-09", 4.0, 15.0
		}
		result, ok := function.Handler(t.Context(), adapter.FunctionRequest{Arguments: json.RawMessage(args)}).(adapter.FunctionSucceeded)
		if !ok {
			t.Fatalf("%s failed", function.Descriptor.Key)
		}
		schema, err := functionschema.Compile(function.Descriptor.OutputSchema)
		if err != nil || schema.Validate(result.Result) != nil {
			t.Fatalf("%s result schema validation: %v", function.Descriptor.Key, err)
		}
		var document struct {
			Periods []map[string]any `json:"periods"`
		}
		if unmarshalErr := json.Unmarshal(result.Result, &document); unmarshalErr != nil {
			t.Fatal(unmarshalErr)
		}
		if len(document.Periods) != 1 || !reflect.DeepEqual(document.Periods[0], want) {
			t.Fatalf("%s period = %v, want %v", function.Descriptor.Key, document.Periods, want)
		}
		assertFunctionRejectsLegacyKeys(t, schema, result.Result, function.Descriptor.Key)
	}
}

func assertFunctionRejectsLegacyKeys(t *testing.T, schema *functionschema.Schema, result json.RawMessage, name string) {
	t.Helper()
	for _, legacy := range []string{"temperature_c", "temperature_min_c", "temperature_max_c", "apparent_temperature_c",
		"humidity_percent", "pressure_hpa", "wind_speed_mps", "wind_gust_mps", "wind_direction_degrees",
		"precipitation_rate_mmh", "precipitation_probability_percent", "cloud_cover_percent"} {
		var payload map[string]any
		if err := json.Unmarshal(result, &payload); err != nil {
			t.Fatal(err)
		}
		payload["periods"].([]any)[0].(map[string]any)[legacy] = 0
		encoded, err := json.Marshal(payload)
		if err != nil || schema.Validate(encoded) == nil {
			t.Errorf("%s accepted legacy key %s", name, legacy)
		}
	}
}

// Synthetic complete periods exercise every State measurement and provider-only fields.
func fullForecastBody(t *testing.T, now time.Time, hours, days int) string {
	t.Helper()
	hourly := make([]map[string]any, hours)
	daily := make([]map[string]any, days)
	period := func(at time.Time) map[string]any {
		return map[string]any{"time": at.Unix(), "summary": "Rain expected", "icon": "rain", "precipType": "rain",
			"humidity": 0.42, "pressure": 1013.0, "windSpeed": 3.0, "windGust": 5.0,
			"windBearing": 180.0, "precipIntensity": 0.5, "precipProbability": 0.7, "cloudCover": 0.3, "uvIndex": 2.0}
	}
	for i := range hourly {
		hourly[i] = period(now.Add(time.Duration(i) * time.Hour))
		hourly[i]["temperature"], hourly[i]["apparentTemperature"] = 12.5, 11.5
	}
	for i := range daily {
		daily[i] = period(now.Add(time.Duration(i) * 24 * time.Hour))
		daily[i]["temperatureMin"], daily[i]["temperatureMax"] = 4.0, 15.0
	}
	body, err := json.Marshal(map[string]any{"flags": map[string]any{"units": "si"}, "timezone": "UTC",
		"currently": map[string]any{"time": now.Unix(), "temperature": 12.345, "apparentTemperature": 11,
			"humidity": 0.42, "pressure": 1013, "windSpeed": 3, "windGust": 5, "windBearing": 180,
			"precipIntensity": 0, "precipProbability": 0.2, "cloudCover": 0.3, "uvIndex": 2},
		"hourly": map[string]any{"data": hourly}, "daily": map[string]any{"data": daily}})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestEmptyDecodedSummaryOmittedFromForecastObservations(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	body := []byte(
		fmt.Sprintf(
			`{"flags":{"units":"si"},"timezone":"UTC","hourly":{"data":[{"time":%d,"temperature":1,"summary":"","icon":"unknown"}]},"daily":{"data":[{"time":%d,"temperatureMin":1,"summary":"","icon":""}]}}`,
			now.Unix(),
			now.Unix(),
		),
	)
	hourly, err := decodeHourly(body, "Home", now, 1)
	if err != nil || hourly.Periods[0].Summary == nil || *hourly.Periods[0].Summary != "" {
		t.Fatalf("decoded hourly: %+v %v", hourly, err)
	}
	daily, err := decodeDaily(body, "Home", now, 1)
	if err != nil || daily.Periods[0].Summary == nil || *daily.Periods[0].Summary != "" {
		t.Fatalf("decoded daily: %+v %v", daily, err)
	}
	for _, makeObservation := range []func() (string, error){
		func() (string, error) {
			o, e := hourlyObservation("ent_019a68f8-0000-7000-8000-000000000001", hourly)
			return string(o.Value), e
		},
		func() (string, error) {
			o, e := dailyObservation("ent_019a68f8-0000-7000-8000-000000000002", daily)
			return string(o.Value), e
		},
	} {
		value, observationErr := makeObservation()
		if observationErr != nil || strings.Contains(value, `"summary"`) || strings.Contains(value, `"condition"`) {
			t.Fatalf("State = %s, error = %v", value, observationErr)
		}
	}
}

func TestFullForecastObservationSize(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	body := []byte(fullForecastBody(t, now, 48, 8))
	hourly, err := decodeHourly(body, "Home", now, 48)
	if err != nil {
		t.Fatal(err)
	}
	daily, err := decodeDaily(body, "Home", now, 8)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hourlyObservation("ent_019a68f8-0000-7000-8000-000000000001", hourly)
	if err != nil {
		t.Fatal(err)
	}
	d, err := dailyObservation("ent_019a68f8-0000-7000-8000-000000000002", daily)
	if err != nil {
		t.Fatal(err)
	}
	he, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	de, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(
		"full forecast State bytes: hourly=%d daily=%d pair=%d; encoded Observation bytes: hourly=%d daily=%d pair=%d",
		len(h.Value),
		len(d.Value),
		len(h.Value)+len(d.Value),
		len(he),
		len(de),
		len(he)+len(de),
	)
}
