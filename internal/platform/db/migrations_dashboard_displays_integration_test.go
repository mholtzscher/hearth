//go:build integration

package db //nolint:testpackage // Tests the embedded migration at its schema boundary.

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"testing"
)

const dashboardDisplaysID = "dash_01890f47-7a6b-7c4d-8e9f-0123456789ab"

// dashboardDisplaysDocument has one element per migration case: humidity and
// pressure Metrics, a speed chart and a brightness Metric move to their named
// display; numericsensor keeps native; temperature is untouched. The element
// key with a dot proves paths quote keys.
const dashboardDisplaysDocument = `{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Sensors","spec":{"root":"g","elements":{` +
	`"g":{"type":"Grid","props":{"columns":2},"children":["h","p.x","s","b","n","t"]},` +
	`"h":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_1","entity_type":"hearth.relativehumidity/v1"},"display":{"unit":"native"}}},` +
	`"p.x":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_2","entity_type":"hearth.pressure/v1"},"display":{"unit":"native"}}},` +
	`"s":{"type":"TimeSeries","props":{"title":"Wind","series":[{"source":{"kind":"entity-series","entity_id":"ent_3","entity_type":"hearth.speed/v1","window":"1h"}}],"display":{"unit":"native"}}},` +
	`"b":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_4","entity_type":"hearth.brightness/v1"},"display":{"unit":"native"}}},` +
	`"n":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_5","entity_type":"hearth.numericsensor/v1"},"display":{"unit":"native"}}},` +
	`"t":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_6","entity_type":"hearth.temperature/v1"},"display":{"unit":"fahrenheit"}}}}}}`

func dashboardDisplayUnits(t *testing.T, raw string) map[string]string {
	t.Helper()
	var document struct {
		Spec struct {
			Elements map[string]struct {
				Props struct {
					Display *struct {
						Unit string `json:"unit"`
					} `json:"display"`
				} `json:"props"`
			} `json:"elements"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	units := map[string]string{}
	for key, element := range document.Spec.Elements {
		if element.Props.Display != nil {
			units[key] = element.Props.Display.Unit
		}
	}
	return units
}

func TestDashboardQuantityDisplayMigrationRewritesNativeUnitsAcrossUpDown(t *testing.T) {
	t.Parallel()
	database, err := Open(t.Context(), filepath.Join(t.TempDir(), "dashboards.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := newGooseProvider(t, database)
	if _, err = provider.UpTo(t.Context(), 27); err != nil {
		t.Fatal(err)
	}
	const stamp = "2026-11-03T12:00:00.000000000Z"
	if _, err = database.ExecContext(t.Context(),
		`INSERT INTO dashboards(id, revision, definition_json, created_at, updated_at) VALUES (?, 4, ?, ?, ?)`,
		dashboardDisplaysID, dashboardDisplaysDocument, stamp, stamp,
	); err != nil {
		t.Fatal(err)
	}
	read := func() (string, int, string) {
		var definition, updated string
		var revision int
		if scanErr := database.QueryRowContext(t.Context(),
			`SELECT definition_json, revision, updated_at FROM dashboards WHERE id = ?`, dashboardDisplaysID,
		).Scan(&definition, &revision, &updated); scanErr != nil {
			t.Fatal(scanErr)
		}
		return definition, revision, updated
	}
	original := dashboardDisplayUnits(t, dashboardDisplaysDocument)

	if _, err = provider.UpTo(t.Context(), 28); err != nil {
		t.Fatal(err)
	}
	definition, revision, updated := read()
	want := map[string]string{
		"h":   "percent",
		"p.x": "hpa",
		"s":   "mps",
		"b":   "percent",
		"n":   "native",
		"t":   "fahrenheit",
	}
	if got := dashboardDisplayUnits(t, definition); !maps.Equal(got, want) {
		t.Fatalf("up units = %v, want %v", got, want)
	}
	if revision != 4 || updated != stamp {
		t.Fatalf("up changed revision %d or updated_at %s", revision, updated)
	}

	if _, err = provider.Down(t.Context()); err != nil {
		t.Fatal(err)
	}
	definition, revision, _ = read()
	if got := dashboardDisplayUnits(t, definition); !maps.Equal(got, original) || revision != 4 {
		t.Fatalf("down units = %v revision %d, want %v", got, revision, original)
	}
}
