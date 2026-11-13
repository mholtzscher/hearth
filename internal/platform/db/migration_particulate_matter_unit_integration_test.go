//go:build integration

package db //nolint:testpackage // Migration tests inspect the migration transaction.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

//nolint:gocognit // Assert the complete migration and rollback guard against the same fixture.
func TestParticulateMatterUnitMigration(t *testing.T) {
	t.Parallel()
	database, ctx := forecastMigrationDatabase(t)
	provider, err := NewMigrationProvider(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 32); err != nil {
		t.Fatal(err)
	}
	forecastExec(
		t,
		database,
		`INSERT INTO adapter_bindings (adapter_id,binding_key,device_id,created_at,updated_at) VALUES ('adapter','purifier','dev_one',?,?)`,
		forecastStamp,
		forecastStamp,
	)
	fixtures := []struct{ id, key, typeID, support string }{
		{
			"ent_pm25",
			"pm25",
			"hearth.numericsensor/v1",
			` { "state": {"unit":"ug/m3","minimum":0,"maximum":1000}, "operations": {} } `,
		},
		{
			"ent_voc",
			"voc",
			"hearth.numericsensor/v1",
			` { "state": {"unit":"ug/m3","minimum":0,"maximum":5000}, "operations": {} } `,
		},
		{
			"ent_esphome",
			"esphome",
			"hearth.numericsensor/v1",
			` { "state": {"unit":"µg/m³","quantity_kind":"particulate_matter"}, "operations": {} } `,
		},
		{"ent_percent", "filter-life", "hearth.numericsensor/v1", ` { "state": {"unit":"%"}, "operations": {} } `},
	}
	for _, fixture := range fixtures {
		forecastExec(
			t,
			database,
			`INSERT INTO entities (id,device_id,name,type_id,support_json,created_at,updated_at) VALUES (?,'dev_one',?,?,?, ?,?)`,
			fixture.id,
			fixture.key,
			fixture.typeID,
			fixture.support,
			forecastStamp,
			forecastStamp,
		)
		forecastExec(
			t,
			database,
			`INSERT INTO adapter_entity_mappings (adapter_id,binding_key,entity_key,entity_id,external_entity_id,created_at,updated_at) VALUES ('adapter','purifier',?,?,?,?,?)`,
			fixture.key,
			fixture.id,
			fixture.id,
			forecastStamp,
			forecastStamp,
		)
	}
	if _, err = provider.UpTo(ctx, 33); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		got := forecastRead(t, database, `SELECT support_json FROM entities WHERE id=?`, fixture.id)
		if fixture.key == "pm25" {
			var actual, expected any
			if err = json.Unmarshal([]byte(got), &actual); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(
				[]byte(
					`{"state":{"unit":"µg/m³","quantity_kind":"particulate_matter","minimum":0,"maximum":1000},"operations":{}}`,
				),
				&expected,
			); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Errorf("migrated support = %s", got)
			}
		} else if got != fixture.support {
			t.Errorf("%s support changed: %q", fixture.key, got)
		}
		if got = forecastRead(
			t,
			database,
			`SELECT updated_at FROM entities WHERE id=?`,
			fixture.id,
		); got != forecastStamp {
			t.Errorf("%s updated_at = %q", fixture.key, got)
		}
	}
	const refusal = "restore the pre-upgrade backup with its matching binary; the PM2.5 unit has no lossless Down migration"
	if _, err = provider.Down(ctx); err == nil || !strings.Contains(err.Error(), refusal) {
		t.Fatalf("Down = %v, want %q", err, refusal)
	}
}
