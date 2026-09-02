package devices //nolint:testpackage // Tests exercise the concrete repository against SQLite.

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

var ownedMappingsFixture = []OwnedMapping{ //nolint:gochecknoglobals // Handwritten repository oracle.
	{
		BindingKey: "light", DeviceID: "dev_01890f47-7a6b-7c4d-8e9f-012345678901",
		EntityKey: "brightness", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-012345678901",
	},
	{
		BindingKey: "light", DeviceID: "dev_01890f47-7a6b-7c4d-8e9f-012345678901",
		EntityKey: "power", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-012345678902",
	},
	{
		BindingKey: "light-1", DeviceID: "dev_01890f47-7a6b-7c4d-8e9f-012345678902",
		EntityKey: "power", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-012345678903",
	},
	{
		BindingKey: "light-10", DeviceID: "dev_01890f47-7a6b-7c4d-8e9f-012345678903",
		EntityKey: "power", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-012345678904",
	},
}

func TestSQLiteListOwnedMappingsScopesOrdersAndPaginatesByCompositePosition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := seededOwnedMappingsRepository(t)

	for _, limit := range []int{1, 2, 200} {
		var got []OwnedMapping
		var after *OwnedMappingPosition
		for {
			page, err := repository.ListOwnedMappings(ctx, ListOwnedMappingsParams{
				AdapterID: "homeassistant", After: after, Limit: limit,
			})
			if err != nil {
				t.Fatalf("limit %d: %v", limit, err)
			}
			if len(page.Items) > limit {
				t.Fatalf("limit %d returned %d items", limit, len(page.Items))
			}
			got = append(got, page.Items...)
			if !page.HasMore {
				break
			}
			if len(page.Items) != limit {
				t.Fatalf("limit %d non-terminal page = %#v", limit, page)
			}
			last := page.Items[len(page.Items)-1]
			after = &OwnedMappingPosition{BindingKey: last.BindingKey, EntityKey: last.EntityKey}
		}
		if !reflect.DeepEqual(got, ownedMappingsFixture) {
			t.Fatalf("limit %d mappings = %#v, want %#v", limit, got, ownedMappingsFixture)
		}
	}

	other, err := repository.ListOwnedMappings(ctx, ListOwnedMappingsParams{
		AdapterID: "simulator", Limit: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantOther := []OwnedMapping{{
		BindingKey: "light", DeviceID: "dev_01890f47-7a6b-7c4d-8e9f-012345678905",
		EntityKey: "power", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-012345678905",
	}}
	if other.HasMore || !reflect.DeepEqual(other.Items, wantOther) {
		t.Fatalf("other Adapter mappings = %#v, want %#v", other, wantOther)
	}
}

func TestSQLiteListOwnedMappingsAcceptsLexicalPositions(t *testing.T) {
	t.Parallel()
	repository := seededOwnedMappingsRepository(t)
	tests := []struct {
		name     string
		position OwnedMappingPosition
		want     []OwnedMapping
	}{
		{
			name: "within Binding",
			position: OwnedMappingPosition{
				BindingKey: "light", EntityKey: "brightness",
			},
			want: ownedMappingsFixture[1:],
		},
		{
			name: "Binding boundary",
			position: OwnedMappingPosition{
				BindingKey: "light", EntityKey: "power",
			},
			want: ownedMappingsFixture[2:],
		},
		{
			name: "nonexistent tuple",
			position: OwnedMappingPosition{
				BindingKey: "light", EntityKey: "middle",
			},
			want: ownedMappingsFixture[1:],
		},
		{
			name: "after final tuple",
			position: OwnedMappingPosition{
				BindingKey: "light-10", EntityKey: "power",
			},
			want: []OwnedMapping{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			page, err := repository.ListOwnedMappings(context.Background(), ListOwnedMappingsParams{
				AdapterID: "homeassistant", After: &test.position, Limit: 200,
			})
			if err != nil {
				t.Fatal(err)
			}
			if page.HasMore || page.Items == nil || !reflect.DeepEqual(page.Items, test.want) {
				t.Fatalf("page = %#v, want non-nil terminal items %#v", page, test.want)
			}
		})
	}
}

func seededOwnedMappingsRepository(t *testing.T) *SQLiteRepository {
	t.Helper()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	for index, mapping := range append(append([]OwnedMapping(nil), ownedMappingsFixture...), OwnedMapping{
		BindingKey: "light", DeviceID: "dev_01890f47-7a6b-7c4d-8e9f-012345678905",
		EntityKey: "power", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-012345678905",
	}) {
		adapterID := "homeassistant"
		if index == len(ownedMappingsFixture) {
			adapterID = "simulator"
		}
		insertOwnedMappingFixture(t, database, adapterID, mapping)
	}
	return NewSQLiteRepository(database, firstLightCatalog(t))
}

func insertOwnedMappingFixture(
	t *testing.T,
	database *sql.DB,
	adapterID string,
	mapping OwnedMapping,
) {
	t.Helper()
	const timestamp = "2026-09-01T12:00:00Z"
	if _, err := database.Exec(`
		INSERT OR IGNORE INTO devices (id, kind, name, created_at, updated_at)
		VALUES (?, 'light', ?, ?, ?)`, mapping.DeviceID, mapping.BindingKey, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT OR IGNORE INTO adapter_bindings (
			adapter_id, binding_key, device_id, external_device_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		adapterID, mapping.BindingKey, mapping.DeviceID,
		adapterID+"."+mapping.BindingKey, timestamp, timestamp,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO entities (id, device_id, name, type_id, support_json, enabled, created_at, updated_at)
		VALUES (?, ?, ?, 'hearth.power/v1', '{}', 1, ?, ?)`,
		mapping.EntityID, mapping.DeviceID, mapping.EntityKey, timestamp, timestamp,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO adapter_entity_mappings (
			adapter_id, binding_key, entity_key, entity_id, external_entity_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		adapterID, mapping.BindingKey, mapping.EntityKey, mapping.EntityID,
		adapterID+"."+mapping.BindingKey+"."+mapping.EntityKey, timestamp, timestamp,
	); err != nil {
		t.Fatal(err)
	}
}
