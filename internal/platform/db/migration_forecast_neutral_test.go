package db //nolint:testpackage // Tests the frozen migration conversion.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNeutralForecastFieldsAndNumericTokens(t *testing.T) {
	t.Parallel()
	const input = `{"periods":[{"temperature_c":1.2300e+2,"apparent_temperature_c":-0.00,"temperature_min_c":1,"temperature_max_c":2,"precipitation_probability_percent":3,"precipitation_rate_mmh":4,"humidity_percent":5,"pressure_hpa":6,"wind_speed_mps":7,"wind_gust_mps":8,"wind_direction_degrees":9,"cloud_cover_percent":10,"uv_index":11,"condition":"clear","summary":"Hi","starts_at":"2026-11-10T00:00:00Z","date":"2026-11-10"}]}`
	got, err := neutralForecast(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{`"temperature":1.2300e+2`, `"apparent_temperature":-0.00`, `"uv_index":11`, `"condition":"clear"`, `"date":"2026-11-10"`} {
		if !strings.Contains(got, token) {
			t.Errorf("converted forecast missing %s: %s", token, got)
		}
	}
	var obj struct {
		Periods []map[string]json.RawMessage `json:"periods"`
	}
	if err = json.Unmarshal([]byte(got), &obj); err != nil {
		t.Fatal(err)
	}
	for _, field := range forecastNeutralFields {
		if _, ok := obj.Periods[0][field[0]]; ok {
			t.Errorf("legacy field %q remains", field[0])
		}
		if _, ok := obj.Periods[0][field[1]]; !ok {
			t.Errorf("neutral field %q missing", field[1])
		}
	}
	const neutral = ` { "periods" : [ { "temperature" : 1e+2, "uv_index": 3 } ] } `
	got, err = neutralForecast(neutral)
	if err != nil || got != neutral {
		t.Fatalf("neutral forecast = %q, %v; want original bytes", got, err)
	}
	if _, err = neutralForecast(`{"periods":[{"temperature_c":1,"temperature":1}]}`); err == nil {
		t.Fatal("mixed legacy and neutral keys accepted")
	}
}
