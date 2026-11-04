//go:build integration

package db //nolint:testpackage // Tests the embedded migration at its schema boundary.

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"testing"
)

const dashboardDisplaysID = "dash_01890f47-7a6b-7c4d-8e9f-0123456789ab"

// dashboardDisplaysDocument is a migration-27 definition: every numeric card
// stores a display, a Grid has none, and one element key contains a dot to
// prove paths quote keys.
const dashboardDisplaysDocument = `{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Sensors","spec":{"root":"g","elements":{` +
	`"g":{"type":"Grid","props":{"columns":2},"children":["h","p.x","s","t"]},` +
	`"h":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_1","entity_type":"hearth.relativehumidity/v1"},"display":{"unit":"native"}}},` +
	`"p.x":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_2","entity_type":"hearth.pressure/v1"},"display":{"unit":"native"}}},` +
	`"s":{"type":"TimeSeries","props":{"title":"Wind","series":[{"source":{"kind":"entity-series","entity_id":"ent_3","entity_type":"hearth.speed/v1","window":"1h"}}],"display":{"unit":"native"}}},` +
	`"t":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ent_6","entity_type":"hearth.temperature/v1"},"display":{"unit":"fahrenheit"}}}}}}`

// dashboardDisplays maps element keys to their display unit; absent means no display.
func dashboardDisplays(t *testing.T, raw string) map[string]string {
	t.Helper()
	var document struct {
		Spec struct {
			Elements map[string]struct {
				Props map[string]json.RawMessage `json:"props"`
			} `json:"elements"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	units := map[string]string{}
	for key, element := range document.Spec.Elements {
		display, ok := element.Props["display"]
		if !ok {
			continue
		}
		var value struct {
			Unit string `json:"unit"`
		}
		if err := json.Unmarshal(display, &value); err != nil {
			t.Fatal(err)
		}
		units[key] = value.Unit
	}
	return units
}

func TestDashboardDisplayRemovalMigrationAcrossUpDown(t *testing.T) {
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

	if _, err = provider.UpTo(t.Context(), 28); err != nil {
		t.Fatal(err)
	}
	definition, revision, updated := read()
	if got := dashboardDisplays(t, definition); len(got) != 0 {
		t.Fatalf("up left displays %v", got)
	}
	if revision != 4 || updated != stamp {
		t.Fatalf("up changed revision %d or updated_at %s", revision, updated)
	}

	if _, err = provider.Down(t.Context()); err != nil {
		t.Fatal(err)
	}
	definition, revision, _ = read()
	want := map[string]string{"h": "native", "p.x": "native", "s": "native", "t": "celsius"}
	if got := dashboardDisplays(t, definition); !maps.Equal(got, want) || revision != 4 {
		t.Fatalf("down displays = %v revision %d, want %v", got, revision, want)
	}
}
