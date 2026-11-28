package agent //nolint:testpackage // Exercises the private tool with the real dashboards compiler.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	formRoom       = "ent_01900000-0000-7000-8000-000000000b01"
	formThermostat = "ent_01900000-0000-7000-8000-000000000b02"
	formHeater     = "ent_01900000-0000-7000-8000-000000000b03"
	formHumidity   = "ent_01900000-0000-7000-8000-000000000b04"
)

func chartFormTool(t *testing.T) composeViewTool {
	t.Helper()
	empty := devices.EntitySupport(`{"state":{},"operations":{}}`)
	thermostat := devices.EntitySupport(
		`{"state":{"modes":["off","heat"],"fan_modes":[],"temperature_minimum":5000,"temperature_maximum":35000,` +
			`"humidity_minimum":0,"humidity_maximum":100},"operations":{"set_mode":{},"set_target":{}}}`,
	)
	inventory := composeInventory{
		formRoom:       {ID: formRoom, TypeID: devices.EntityTypeTemperatureV1, Support: empty},
		formThermostat: {ID: formThermostat, TypeID: devices.EntityTypeThermostatV1, Support: thermostat},
		formHeater:     {ID: formHeater, TypeID: devices.EntityTypePowerV1, Support: empty},
		formHumidity:   {ID: formHumidity, TypeID: devices.EntityTypeRelativehumidityV1, Support: empty},
	}
	return testComposeTool(t, dashboards.NewService(nil, inventory, inventory))
}

func TestComposeViewCreatesEveryChartForm(t *testing.T) {
	t.Parallel()
	layout := `{"type":"Stack","children":[
		{"type":"TimeSeries","title":"Heat","window":"24h","series":[
			{"entity_id":"` + formThermostat + `","field":"/target_temperature","label":"Target"},
			{"entity_id":"` + formThermostat + `","field":"/current_temperature","label":"Room"},
			{"entity_id":"` + formRoom + `"}]},
		{"type":"TimeSeries","title":"Today","window":"24h","compare":"typical","series":[{"entity_id":"` + formRoom + `"}]},
		{"type":"Ranking","title":"Warmest","range":"24h","rows":[
			{"entity_id":"` + formRoom + `"},{"entity_id":"` + formThermostat + `","field":"/current_temperature"}]},
		{"type":"WeekGrid","entity_id":"` + formRoom + `"},
		{"type":"TimeInRange","entity_id":"` + formRoom + `","window":"7d","thresholds":[20000.0,22500.5]},
		{"type":"PeriodTotals","entity_id":"` + formHeater + `","period":"hour","style":"columns"}]}`
	result, err := chartFormTool(t).InvokableRun(t.Context(), `{"mode":"create","title":"Forms","layout":`+layout+`}`)
	require.NoError(t, err)
	artifact, err := dashboards.ParseArtifact([]byte(result))
	require.NoError(t, err, "tool result: %s", result)
	require.Contains(t, string(artifact.Definition), `"thresholds":[20000,22500.5]`)
	require.Contains(t, string(artifact.Definition), `"compare":"typical"`)
}

func TestComposeViewRejectsUnitsAndCompareMisuse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, card, reason, path string }{
		{
			"different canonical units",
			`{"type":"TimeSeries","title":"T","window":"24h","series":[{"entity_id":"` + formRoom + `"},{"entity_id":"` + formHumidity + `"}]}`,
			"share one unit", "/layout/series/1/entity_id",
		},
		{
			"compare with two series",
			`{"type":"TimeSeries","title":"T","window":"24h","compare":"typical","series":[{"entity_id":"` + formRoom + `"},{"entity_id":"` + formThermostat + `","field":"/current_temperature"}]}`,
			"compare needs exactly one series", "/layout/compare",
		},
		{
			"compare with 6h",
			`{"type":"TimeSeries","title":"T","window":"6h","compare":"typical","series":[{"entity_id":"` + formRoom + `"}]}`,
			"compare needs exactly one series", "/layout/compare",
		},
		{
			"descending thresholds",
			`{"type":"TimeInRange","entity_id":"` + formRoom + `","window":"24h","thresholds":[22000,20000.5]}`,
			"strictly ascending", "/layout/thresholds",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			output, failure := invokedComposeFailure(
				t, chartFormTool(t), `{"mode":"create","title":"View","layout":`+tc.card+`}`,
			)
			require.Equal(t, "invalid_definition", failure.Code, "tool output: %s", output)
			require.Contains(t, failure.Message, tc.reason)
			require.Equal(t, []composeDiagnostic{{Code: "invalid_value", Path: tc.path}}, failure.Diagnostics)
			require.NotContains(t, output, formRoom)
		})
	}
}

func TestComposeViewSchemaBoundsChartForms(t *testing.T) {
	t.Parallel()
	for name, card := range map[string]string{
		"one ranking row":    `{"type":"Ranking","title":"R","rows":[{"entity_id":"` + formRoom + `"}]}`,
		"three thresholds":   `{"type":"TimeInRange","entity_id":"` + formRoom + `","window":"24h","thresholds":[1,2,3]}`,
		"no thresholds":      `{"type":"TimeInRange","entity_id":"` + formRoom + `","window":"24h","thresholds":[]}`,
		"unknown compare":    `{"type":"TimeSeries","title":"T","window":"24h","compare":"yesterday","series":[{"entity_id":"` + formRoom + `"}]}`,
		"period needs style": `{"type":"PeriodTotals","entity_id":"` + formHeater + `","period":"day"}`,
		"unknown period":     `{"type":"PeriodTotals","entity_id":"` + formHeater + `","period":"week","style":"ledger"}`,
		"label on source":    `{"type":"WeekGrid","entity_id":"` + formRoom + `","source":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, failure := invokedComposeFailure(t, chartFormTool(t), `{"mode":"create","title":"V","layout":`+card+`}`)
			require.Equal(t, "invalid_arguments", failure.Code)
		})
	}
}

func TestComposeViewNormalizationKeepsFractionalNumbers(t *testing.T) {
	t.Parallel()
	// The tool decodes with json.Number; whole numbers become ints, others stay.
	decoder := json.NewDecoder(strings.NewReader(`{"a":[20.5,8.0,-3,1e3,0.25,12345678901234567890]}`))
	decoder.UseNumber()
	var numbers map[string]any
	require.NoError(t, decoder.Decode(&numbers))
	normalized := normalizeComposeIntegers(numbers).(map[string]any)["a"].([]any)
	require.Equal(t, []any{20.5, 8, -3, 1000, 0.25, 12345678901234567890.0}, normalized)
}
