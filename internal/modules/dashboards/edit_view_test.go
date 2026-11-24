package dashboards_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const selectedViewFixture = `{
	"schema_version": 1,
	"catalog_version": "hearth.dashboard/v1",
	"title": "Selected draft",
	"spec": {
		"root": "pane",
		"elements": {
			"pane": {"type":"Stack","props":{"gap":"large"},"children":["forecast", "rest"]},
			"forecast": {"type":"Forecast","props":{"source":{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000201","entity_type":"hearth.dailyforecast/v1","field":""},"periods":7,"label":"Home weather"}},
			"rest": {"type":"Grid","props":{"columns":2},"children":["chart","notes"]},
			"chart": {"type":"TimeSeries","props":{"title":"Cover position","series":[
				{"label":"West","source":{"kind":"entity-series","entity_id":"ent_01900000-0000-7000-8000-000000000204","entity_type":"hearth.cover/v1","field":"/position","window":"24h"}},
				{"label":"East","source":{"kind":"entity-series","entity_id":"ent_01900000-0000-7000-8000-000000000205","entity_type":"hearth.cover/v1","field":"/position","window":"24h"}}
			]}},
			"notes": {"type":"Cover","props":{"layout":"tile","confirm_open":false,"label":"Bedroom shade","source":{"kind":"entity-state","entity_id":"ent_01900000-0000-7000-8000-000000000206","entity_type":"hearth.cover/v1","field":""}}}
		}
	}
}`

func TestEditViewKeepsUntargetedValuesAndDoesNotSave(t *testing.T) {
	t.Parallel()
	selected, reader := selectedView(t)
	original := append(json.RawMessage(nil), selected.Spec...)
	repository := &bindingWriteRecorder{}
	service := dashboards.NewService(repository, reader, nil)

	changes := parseViewChanges(t, `[
		{"op":"set_title","title":"Evening dashboard"},
		{"op":"set_forecast_periods","card_id":"forecast","periods":3},
		{"op":"set_chart_window","card_id":"chart","window":"7d"}
	]`)
	artifact, err := service.EditView(t.Context(), selected, changes)
	require.NoError(t, err)
	require.Equal(t, "hearth.view", artifact.Kind)
	expected := strings.NewReplacer(
		`"Selected draft"`, `"Evening dashboard"`,
		`"periods":7`, `"periods":3`,
		`"window":"24h"`, `"window":"7d"`,
	).Replace(selectedViewFixture)
	require.JSONEq(t, expected, string(artifact.Definition))
	require.Equal(t, "Selected draft", selected.Title)
	require.Equal(t, original, selected.Spec)
	require.Equal(t, &bindingWriteRecorder{}, repository)
}

func TestEditViewRejectsWholeInvalidBatch(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		second string
		path   string
	}{
		{"wrong kind", `{"op":"set_forecast_periods","card_id":"notes","periods":6}`, "/changes/1/card_id"},
		{"not in draft", `{"op":"set_chart_window","card_id":"chart-from-another-draft","window":"7d"}`, "/changes/1/card_id"},
		{"invalid daily count", `{"op":"set_forecast_periods","card_id":"forecast","periods":12}`, "/changes/1/periods"},
		{"duplicate target", `{"op":"set_title","title":"Oops"}`, "/changes/1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			selected, reader := selectedView(t)
			original := append(json.RawMessage(nil), selected.Spec...)
			changes := parseViewChanges(t, `[{"op":"set_title","title":"First change"},`+test.second+`]`)
			repository := &bindingWriteRecorder{}
			artifact, err := dashboards.NewService(repository, reader, nil).EditView(t.Context(), selected, changes)
			require.ErrorIs(t, err, dashboards.ErrInvalidDefinition)
			var argument dashboards.ViewArgumentError
			require.ErrorAs(t, err, &argument)
			require.Equal(t, test.path, argument.Path)
			require.Equal(t, dashboards.Artifact{}, artifact)
			require.Equal(t, "Selected draft", selected.Title)
			require.Equal(t, original, selected.Spec)
			require.Equal(t, &bindingWriteRecorder{}, repository)
		})
	}
}

func TestEditViewRechecksAnUntouchedBinding(t *testing.T) {
	t.Parallel()
	selected, reader := selectedView(t)
	const changed = "ent_01900000-0000-7000-8000-000000000206"
	reader[changed] = devices.EntityWithState{Entity: devices.Entity{ID: changed, TypeID: devices.EntityTypePowerV1}}
	artifact, err := dashboards.NewService(nil, reader, nil).EditView(
		t.Context(), selected, parseViewChanges(t, `[{"op":"set_title","title":"New name"}]`),
	)
	require.ErrorIs(t, err, dashboards.ErrInvalidDefinition)
	require.ErrorContains(t, err, "entity_type must match the Entity")
	require.ErrorContains(t, err, "dashboard editor")
	require.Equal(t, dashboards.Artifact{}, artifact)
}

func selectedView(t *testing.T) (dashboards.Definition, chartReader) {
	t.Helper()
	definition, err := dashboards.ParseDefinition(json.RawMessage(selectedViewFixture))
	require.NoError(t, err)
	reader := exampleEntities(json.RawMessage(selectedViewFixture))
	for id, item := range reader {
		if item.Entity.TypeID == devices.EntityTypeCoverV1 {
			item.Entity.Support = devices.EntitySupport(`{"state":{},"operations":{"set_position":{}}}`)
			reader[id] = item
		}
	}
	return definition, reader
}

func parseViewChanges(t *testing.T, raw string) []dashboards.ViewChange {
	t.Helper()
	var changes []dashboards.ViewChange
	require.NoError(t, json.Unmarshal([]byte(raw), &changes))
	return changes
}
