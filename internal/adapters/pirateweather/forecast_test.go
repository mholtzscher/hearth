package pirateweather //nolint:testpackage // Exercises forecast conversion at the provider boundary.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
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
