package dashboards_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	purifierPower     = "ent_01900000-0000-7000-8000-000000000301"
	purifierPM25      = "ent_01900000-0000-7000-8000-000000000302"
	purifierFilter    = "ent_01900000-0000-7000-8000-000000000303"
	purifierMode      = "ent_01900000-0000-7000-8000-000000000304"
	purifierFan       = "ent_01900000-0000-7000-8000-000000000305"
	purifierDisplay   = "ent_01900000-0000-7000-8000-000000000306"
	purifierChildLock = "ent_01900000-0000-7000-8000-000000000307"
	purifierDevice    = "dev_01900000-0000-7000-8000-000000000301"
)

func purifierSource(id, entityType string) string {
	return `{"kind":"entity-state","entity_id":"` + id + `","entity_type":"` + entityType + `"}`
}

func purifierNode(layout string, roles map[string]string) string {
	props := []string{`"layout":"` + layout + `"`}
	for _, role := range []string{"power", "pm25", "filter_life", "mode", "fan_level", "display", "child_lock"} {
		if source, ok := roles[role]; ok {
			props = append(props, `"`+role+`":`+source)
		}
	}
	return `{"type":"AirPurifier","props":{` + strings.Join(props, ",") + `}}`
}

func fullPurifierRoles() map[string]string {
	return map[string]string{
		"power":       purifierSource(purifierPower, "hearth.power/v1"),
		"pm25":        purifierSource(purifierPM25, "hearth.numericsensor/v1"),
		"filter_life": purifierSource(purifierFilter, "hearth.numericsensor/v1"),
		"mode":        purifierSource(purifierMode, "hearth.enumsetting/v1"),
		"fan_level":   purifierSource(purifierFan, "hearth.enumsetting/v1"),
		"display":     purifierSource(purifierDisplay, "hearth.power/v1"),
		"child_lock":  purifierSource(purifierChildLock, "hearth.power/v1"),
	}
}

func purifierEntity(id, entityType, support string) devices.EntityWithState {
	return devices.EntityWithState{Entity: devices.Entity{
		ID:       devices.EntityID(id),
		DeviceID: purifierDevice,
		TypeID:   devices.EntityTypeID(entityType),
		Support:  devices.EntitySupport(support),
	}}
}

// purifierReader is a Levoit-shaped air purifier: every role on one Device.
func purifierReader() chartReader {
	const powerSupport = `{"state":{},"operations":{"set":{}}}`
	return chartReader{
		purifierPower: purifierEntity(purifierPower, "hearth.power/v1", powerSupport),
		purifierPM25: purifierEntity(
			purifierPM25,
			"hearth.numericsensor/v1",
			`{"state":{"minimum":0,"maximum":1000,"unit":"µg/m³","quantity_kind":"particulate_matter"},"operations":{}}`,
		),
		purifierFilter: purifierEntity(purifierFilter, "hearth.numericsensor/v1",
			`{"state":{"minimum":0,"maximum":100,"unit":"%"},"operations":{}}`),
		purifierMode: purifierEntity(purifierMode, "hearth.enumsetting/v1",
			`{"state":{"choices":["manual","auto","sleep"]},"operations":{"set":{}}}`),
		purifierFan: purifierEntity(purifierFan, "hearth.enumsetting/v1",
			`{"state":{"choices":["1","2","3","4"]},"operations":{"set":{}}}`),
		purifierDisplay:   purifierEntity(purifierDisplay, "hearth.power/v1", powerSupport),
		purifierChildLock: purifierEntity(purifierChildLock, "hearth.power/v1", powerSupport),
	}
}

func presentCreateSave(t *testing.T, d dashboards.Definition, reader dashboards.EntityReader) []error {
	t.Helper()
	service := dashboards.NewService(&bindingWriteRecorder{}, reader, nil)
	_, presentErr := service.PresentView(context.Background(), d)
	_, createErr := service.CreateDashboard(context.Background(), d)
	_, saveErr := service.SaveDashboard(context.Background(), dashboards.SaveDashboardParams{
		ID:               "dash_01900000-0000-7000-8000-000000000001",
		ExpectedRevision: 1,
		Definition:       d,
	})
	return []error{presentErr, createErr, saveErr}
}

func TestAirPurifierAcceptedAtEveryWritePath(t *testing.T) {
	t.Parallel()
	minimal := map[string]string{
		"power": purifierSource(purifierPower, "hearth.power/v1"),
		"pm25":  purifierSource(purifierPM25, "hearth.numericsensor/v1"),
	}
	for _, tc := range []struct {
		name, node string
	}{
		{"full card", purifierNode("card", fullPurifierRoles())},
		{"minimal tile", purifierNode("tile", minimal)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, err := range presentCreateSave(t, cardDefinition(t, tc.node), purifierReader()) {
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestAirPurifierRejectsMismatchedRoles(t *testing.T) {
	t.Parallel()
	const (
		pm25Unit   = "AirPurifier PM2.5 needs a numeric sensor in µg/m³"
		filterUnit = "AirPurifier filter life needs a numeric sensor in %"
		modes      = "AirPurifier modes need an enum setting with 2 to 5 choices"
		levels     = "AirPurifier fan levels need an enum setting whose choices are 1 to N"
		elsewhere  = "AirPurifier Entities must belong to the power Entity's Device"
	)
	withSupport := func(id, support string) func(chartReader) {
		return func(r chartReader) {
			entity := r[devices.EntityID(id)]
			entity.Entity.Support = devices.EntitySupport(support)
			r[devices.EntityID(id)] = entity
		}
	}
	for _, tc := range []struct {
		name   string
		change func(chartReader)
		reason string
	}{
		{"legacy PM2.5 unit", withSupport(purifierPM25, `{"state":{"minimum":0,"maximum":1000,"unit":"ug/m3"},"operations":{}}`), pm25Unit},
		{"filter in another unit", withSupport(purifierFilter, `{"state":{"minimum":0,"maximum":1,"unit":"ratio"},"operations":{}}`), filterUnit},
		{"one mode", withSupport(purifierMode, `{"state":{"choices":["auto"]},"operations":{"set":{}}}`), modes},
		{"six modes", withSupport(purifierMode, `{"state":{"choices":["a","b","c","d","e","f"]},"operations":{"set":{}}}`), modes},
		{"levels from zero", withSupport(purifierFan, `{"state":{"choices":["0","1","2"]},"operations":{"set":{}}}`), levels},
		{"levels out of order", withSupport(purifierFan, `{"state":{"choices":["1","3","2"]},"operations":{"set":{}}}`), levels},
		{"named levels", withSupport(purifierFan, `{"state":{"choices":["low","high"]},"operations":{"set":{}}}`), levels},
		{"eleven levels", withSupport(purifierFan, `{"state":{"choices":["1","2","3","4","5","6","7","8","9","10","11"]},"operations":{"set":{}}}`), levels},
		{"child lock on another Device", func(r chartReader) {
			entity := r[purifierChildLock]
			entity.Entity.DeviceID = "dev_01900000-0000-7000-8000-000000000399"
			r[purifierChildLock] = entity
		}, elsewhere},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := purifierReader()
			tc.change(reader)
			for _, err := range presentCreateSave(t, cardDefinition(t, purifierNode("card", fullPurifierRoles())), reader) {
				var invalid dashboards.DefinitionError
				if !errors.As(err, &invalid) || invalid.Reason != tc.reason {
					t.Fatalf("error = %v, want reason %q", err, tc.reason)
				}
			}
		})
	}
}

func TestAirPurifierRejectsAnEntityInTwoRoles(t *testing.T) {
	t.Parallel()
	roles := fullPurifierRoles()
	roles["child_lock"] = purifierSource(purifierDisplay, "hearth.power/v1")
	_, err := dashboards.ParseDefinition(json.RawMessage(
		`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Air","spec":{"root":"card","elements":{"card":` +
			purifierNode(
				"card",
				roles,
			) + `}}}`,
	))
	var invalid dashboards.DefinitionError
	if !errors.As(err, &invalid) || invalid.Reason != "each AirPurifier role must name a different Entity" {
		t.Fatalf("error = %v", err)
	}
}
