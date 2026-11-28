package dashboards_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	formTemperature = "ent_01900000-0000-7000-8000-000000000a01"
	formThermostat  = "ent_01900000-0000-7000-8000-000000000a02"
	formPower       = "ent_01900000-0000-7000-8000-000000000a03"
	formHumidity    = "ent_01900000-0000-7000-8000-000000000a04"
	formRainDepth   = "ent_01900000-0000-7000-8000-000000000a05"
	formRainRate    = "ent_01900000-0000-7000-8000-000000000a06"
	formOtherDepth  = "ent_01900000-0000-7000-8000-000000000a07"
	formColorMode   = "ent_01900000-0000-7000-8000-000000000a08"
)

func chartFormReader() chartReader {
	entity := func(id string, typeID devices.EntityTypeID, support string) devices.EntityWithState {
		return devices.EntityWithState{Entity: devices.Entity{
			ID: devices.EntityID(id), TypeID: typeID, Support: devices.EntitySupport(support),
		}}
	}
	sensor := func(unit string) string {
		return `{"state":{"minimum":0,"maximum":10000,"unit":"` + unit + `"},"operations":{}}`
	}
	return chartReader{
		formTemperature: entity(
			formTemperature,
			devices.EntityTypeTemperatureV1,
			exampleSupport["hearth.temperature/v1"],
		),
		formThermostat: entity(formThermostat, devices.EntityTypeThermostatV1, exampleSupport["hearth.thermostat/v1"]),
		formPower:      entity(formPower, devices.EntityTypePowerV1, exampleSupport["hearth.power/v1"]),
		formHumidity: entity(
			formHumidity,
			devices.EntityTypeRelativehumidityV1,
			exampleSupport["hearth.relativehumidity/v1"],
		),
		formRainDepth:  entity(formRainDepth, devices.EntityTypeNumericsensorV1, sensor("mm")),
		formRainRate:   entity(formRainRate, devices.EntityTypeNumericsensorV1, sensor("mm/h")),
		formOtherDepth: entity(formOtherDepth, devices.EntityTypeNumericsensorV1, sensor("mm")),
		formColorMode:  entity(formColorMode, devices.EntityTypeColormodeV1, exampleSupport["hearth.colormode/v1"]),
	}
}

func composeChartForm(t *testing.T, card string) (dashboards.Artifact, error) {
	t.Helper()
	var input dashboards.ComposeViewInput
	require.NoError(t, json.Unmarshal([]byte(`{"title":"Forms","layout":`+card+`}`), &input))
	reader := chartFormReader()
	return dashboards.NewService(nil, reader, reader).ComposeView(t.Context(), input)
}

func source(id, entityType, field, window string) string {
	kind := `"kind":"entity-state"`
	if window != "" {
		kind = `"kind":"entity-series"`
	}
	text := `{` + kind + `,"entity_id":"` + id + `","entity_type":"` + entityType + `"`
	if field != "" {
		text += `,"field":"` + field + `"`
	}
	if window != "" {
		text += `,"window":"` + window + `"`
	}
	return text + `}`
}

func TestComposeViewCompilesChartForms(t *testing.T) {
	t.Parallel()
	temperature, thermostat := "hearth.temperature/v1", "hearth.thermostat/v1"
	for _, tc := range []struct{ name, card, cardType, props string }{
		{
			"Ranking",
			`{"type":"Ranking","title":"Warmest","range":"24h","rows":[` +
				`{"entity_id":"` + formTemperature + `","label":"Kitchen"},` +
				`{"entity_id":"` + formThermostat + `","field":"/current_temperature"}]}`,
			"Ranking",
			`{"title":"Warmest","range":"24h","rows":[` +
				`{"label":"Kitchen","source":` + source(formTemperature, temperature, "", "") + `},` +
				`{"source":` + source(formThermostat, thermostat, "/current_temperature", "") + `}]}`,
		},
		{
			"WeekGrid",
			`{"type":"WeekGrid","entity_id":"` + formTemperature + `","label":"Living room"}`,
			"WeekGrid",
			`{"label":"Living room","source":` + source(formTemperature, temperature, "", "") + `}`,
		},
		{
			"TimeInRange",
			`{"type":"TimeInRange","entity_id":"` + formTemperature + `","window":"7d","thresholds":[20000,22500.5]}`,
			"TimeInRange",
			`{"source":` + source(formTemperature, temperature, "", "") + `,"window":"7d","thresholds":[20000,22500.5]}`,
		},
		{
			"PeriodTotals",
			`{"type":"PeriodTotals","entity_id":"` + formPower + `","words":{"true":"On","false":"Off"},"period":"day","style":"ledger"}`,
			"PeriodTotals",
			`{"source":` + source(formPower, "hearth.power/v1", "", "") + `,"words":{"true":"On","false":"Off"},"period":"day","style":"ledger"}`,
		},
		{
			"TimeSeries mixed types and fields",
			`{"type":"TimeSeries","title":"Heat","window":"6h","series":[` +
				`{"entity_id":"` + formThermostat + `","field":"/target_temperature"},` +
				`{"entity_id":"` + formThermostat + `","field":"/current_temperature"},` +
				`{"entity_id":"` + formTemperature + `"}]}`,
			"TimeSeries",
			`{"title":"Heat","series":[` +
				`{"source":` + source(formThermostat, thermostat, "/target_temperature", "6h") + `},` +
				`{"source":` + source(formThermostat, thermostat, "/current_temperature", "6h") + `},` +
				`{"source":` + source(formTemperature, temperature, "", "6h") + `}]}`,
		},
		{
			"TimeSeries compare",
			`{"type":"TimeSeries","title":"Today","window":"24h","compare":"typical","series":[{"entity_id":"` + formTemperature + `"}]}`,
			"TimeSeries",
			`{"title":"Today","compare":"typical","series":[{"source":` + source(formTemperature, temperature, "", "24h") + `}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			artifact, err := composeChartForm(t, tc.card)
			require.NoError(t, err)
			var definition dashboards.Definition
			require.NoError(t, json.Unmarshal(artifact.Definition, &definition))
			var graph struct {
				Elements map[string]struct {
					Type  string          `json:"type"`
					Props json.RawMessage `json:"props"`
				} `json:"elements"`
			}
			require.NoError(t, json.Unmarshal(definition.Spec, &graph))
			card := graph.Elements["node-1"]
			require.Equal(t, tc.cardType, card.Type)
			require.JSONEq(t, tc.props, string(card.Props))
		})
	}
}

func TestComposeViewRejectsInvalidChartForms(t *testing.T) {
	t.Parallel()
	row := func(id, field string) string {
		if field == "" {
			return `{"entity_id":"` + id + `"}`
		}
		return `{"entity_id":"` + id + `","field":"` + field + `"}`
	}
	for _, tc := range []struct{ name, card, reason, path string }{
		{
			"series with different units from support",
			`{"type":"TimeSeries","title":"T","window":"24h","series":[` + row(formRainDepth, "") + `,` + row(formRainRate, "") + `]}`,
			"a TimeSeries charts series that share one unit", "",
		},
		{
			"series with different fixed units",
			`{"type":"TimeSeries","title":"T","window":"24h","series":[` + row(formTemperature, "") + `,` + row(formHumidity, "") + `]}`,
			"a TimeSeries charts series that share one unit", "/layout/series/1/entity_id",
		},
		{
			"repeated Entity field",
			`{"type":"TimeSeries","title":"T","window":"24h","series":[` + row(formTemperature, "") + `,` + row(formTemperature, "") + `]}`,
			"a TimeSeries needs distinct Entity fields", "/layout/series/1/entity_id",
		},
		{
			"compare with two series",
			`{"type":"TimeSeries","title":"T","window":"24h","compare":"typical","series":[` + row(formTemperature, "") + `,` + row(formThermostat, "/current_temperature") + `]}`,
			"compare needs exactly one series over a 24h window", "/layout/compare",
		},
		{
			"compare with another window",
			`{"type":"TimeSeries","title":"T","window":"6h","compare":"typical","series":[` + row(formTemperature, "") + `]}`,
			"compare needs exactly one series over a 24h window", "/layout/compare",
		},
		{
			"ranking mixed units from support",
			`{"type":"Ranking","title":"R","rows":[` + row(formRainDepth, "") + `,` + row(formRainRate, "") + `]}`,
			"a Ranking compares readings that share one unit", "",
		},
		{
			"ranking mixed fixed units",
			`{"type":"Ranking","title":"R","rows":[` + row(formTemperature, "") + `,` + row(formHumidity, "") + `]}`,
			"a Ranking compares readings that share one unit", "/layout/rows/1/entity_id",
		},
		{
			"ranking repeated field",
			`{"type":"Ranking","title":"R","rows":[` + row(formRainDepth, "") + `,` + row(formRainDepth, "") + `]}`,
			"a Ranking needs distinct Entity fields", "/layout/rows/1/entity_id",
		},
		{
			"ranking boolean row",
			`{"type":"Ranking","title":"R","rows":[` + row(formTemperature, "") + `,` + row(formPower, "") + `]}`,
			"does not match the view schema", "",
		},
		{
			"descending thresholds",
			`{"type":"TimeInRange","entity_id":"` + formTemperature + `","window":"24h","thresholds":[22000,20000]}`,
			"TimeInRange thresholds must be finite and strictly ascending", "/layout/thresholds",
		},
		{
			"period totals text field",
			`{"type":"PeriodTotals","entity_id":"` + formColorMode + `","period":"day","style":"columns"}`,
			"PeriodTotals needs a yes/no field", "",
		},
		{
			"week grid boolean field",
			`{"type":"WeekGrid","entity_id":"` + formPower + `"}`,
			"does not match the view schema", "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := composeChartForm(t, tc.card)
			require.ErrorIs(t, err, dashboards.ErrInvalidDefinition)
			var rejected dashboards.DefinitionError
			require.ErrorAs(t, err, &rejected)
			require.Contains(t, rejected.Reason, tc.reason)
			require.NotContains(t, err.Error(), "ent_0190")
			var argument dashboards.ViewArgumentError
			if tc.path == "" {
				require.NotErrorAs(t, err, &argument)
				return
			}
			require.ErrorAs(t, err, &argument)
			require.Equal(t, tc.path, argument.Path)
		})
	}
}

func TestParseDefinitionRejectsRankingOfFixedUnitsThatDiffer(t *testing.T) {
	t.Parallel()
	row := func(id, entityType string) string { return `{"source":` + source(id, entityType, "", "") + `}` }
	raw := json.RawMessage(
		`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"T","spec":{"root":"card","elements":{"card":` +
			`{"type":"Ranking","props":{"title":"R","rows":[` + row(
			formTemperature,
			"hearth.temperature/v1",
		) + `,` +
			row(
				formHumidity,
				"hearth.relativehumidity/v1",
			) + `]}}}}}`,
	)
	_, err := dashboards.ParseDefinition(raw)
	var rejected dashboards.DefinitionError
	require.ErrorAs(t, err, &rejected)
	require.Equal(t, "a Ranking compares distinct Entity fields in one unit", rejected.Reason)
}

func TestParseDefinitionAcceptsOnlyAscendingFiniteThresholds(t *testing.T) {
	t.Parallel()
	node := func(thresholds string) string {
		return `{"type":"TimeInRange","props":{"source":` + source(formTemperature, "hearth.temperature/v1", "", "") +
			`,"window":"24h","thresholds":` + thresholds + `}}`
	}
	for thresholds, valid := range map[string]bool{
		"[20000]": true, "[20000,22000]": true, "[1.5,2.5]": true, "[-5000,0]": true,
		"[22000,20000]": false, "[5,5]": false, "[1e999]": false,
	} {
		raw := json.RawMessage(
			`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"T","spec":{"root":"card","elements":{"card":` +
				node(
					thresholds,
				) + `}}}`,
		)
		_, err := dashboards.ParseDefinition(raw)
		require.Equal(t, valid, err == nil, thresholds)
		if !valid {
			var rejected dashboards.DefinitionError
			require.ErrorAs(t, err, &rejected, thresholds)
		}
	}
}

const chartFormsView = `{
	"schema_version": 1, "catalog_version": "hearth.dashboard/v1", "title": "Forms",
	"spec": {"root": "stack", "elements": {
		"stack": {"type":"Stack","props":{"gap":"medium"},"children":["range","ranking","typical","trend"]},
		"range": {"type":"TimeInRange","props":{"label":"Living room","source":` +
	`{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000a01","entity_type":"hearth.temperature/v1"},` +
	`"window":"24h","thresholds":[20000.5,22000]}},
		"ranking": {"type":"Ranking","props":{"title":"Warmest","rows":[` +
	`{"source":{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000a01","entity_type":"hearth.temperature/v1"}},` +
	`{"source":{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000a02","entity_type":"hearth.thermostat/v1","field":"/current_temperature"}}]}},
		"typical": {"type":"TimeSeries","props":{"title":"Today","compare":"typical","series":[{"source":` +
	`{"kind":"entity-series","entity_id":"ent_01900000-0000-7000-8000-000000000a01","entity_type":"hearth.temperature/v1","window":"24h"}}]}},
		"trend": {"type":"TimeSeries","props":{"title":"Trend","series":[{"source":` +
	`{"kind":"entity-series","entity_id":"ent_01900000-0000-7000-8000-000000000a01","entity_type":"hearth.temperature/v1","window":"24h"}}]}}
	}}
}`

func editChartForms(t *testing.T, changes string) (string, error) {
	t.Helper()
	selected, err := dashboards.ParseDefinition(json.RawMessage(chartFormsView))
	require.NoError(t, err)
	reader := chartFormReader()
	artifact, err := dashboards.NewService(nil, reader, reader).EditView(
		t.Context(), selected, parseViewChanges(t, changes),
	)
	return string(artifact.Definition), err
}

func TestEditViewSetsChartWindowOnTimeInRangeAndRanking(t *testing.T) {
	t.Parallel()
	t.Run("TimeInRange window only", func(t *testing.T) {
		t.Parallel()
		edited, err := editChartForms(t, `[{"op":"set_chart_window","card_id":"range","window":"7d"}]`)
		require.NoError(t, err)
		want := strings.Replace(chartFormsView, `"window":"24h","thresholds"`, `"window":"7d","thresholds"`, 1)
		require.JSONEq(t, want, edited)
	})
	t.Run("Ranking range", func(t *testing.T) {
		t.Parallel()
		edited, err := editChartForms(t, `[{"op":"set_chart_window","card_id":"ranking","window":"6h"}]`)
		require.NoError(t, err)
		want := strings.Replace(chartFormsView, `"title":"Warmest","rows"`, `"title":"Warmest","range":"6h","rows"`, 1)
		require.JSONEq(t, want, edited)
	})
	t.Run("TimeSeries without compare", func(t *testing.T) {
		t.Parallel()
		edited, err := editChartForms(t, `[{"op":"set_chart_window","card_id":"trend","window":"7d"}]`)
		require.NoError(t, err)
		var graph struct {
			Spec struct {
				Elements map[string]struct {
					Props json.RawMessage `json:"props"`
				} `json:"elements"`
			} `json:"spec"`
		}
		require.NoError(t, json.Unmarshal([]byte(edited), &graph))
		require.Contains(t, string(graph.Spec.Elements["trend"].Props), `"window":"7d"`)
		require.Contains(t, string(graph.Spec.Elements["typical"].Props), `"window":"24h"`)
	})
}

func TestEditViewRejectsWindowChangesItCannotMake(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, card, reason string }{
		{"compare keeps 24h", "typical", "a TimeSeries with compare keeps its 24h window"},
		{"not a chart", "stack", "choose a TimeSeries or TimeInRange or Ranking card key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := editChartForms(t, `[{"op":"set_chart_window","card_id":"`+tc.card+`","window":"6h"}]`)
			require.ErrorIs(t, err, dashboards.ErrInvalidDefinition)
			require.ErrorContains(t, err, tc.reason)
			var argument dashboards.ViewArgumentError
			require.ErrorAs(t, err, &argument)
			require.Equal(t, "/changes/0/card_id", argument.Path)
		})
	}
}
