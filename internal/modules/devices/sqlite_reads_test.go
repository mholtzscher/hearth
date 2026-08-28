package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

const (
	readDeviceA  = DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789aa")
	readDeviceB  = DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	readEntityA  = EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789a1")
	readEntityB  = EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789a2")
	readEntityC  = EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789a3")
	readCommandA = CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789a1")
	readCommandB = CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789a2")
)

//nolint:gocognit,gocyclo,cyclop // Related keyset pagination invariants are intentionally verified together.
func TestSQLiteResourceReadsUseDeterministicKeysetPages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewSQLiteRepository(database, nil)
	requestedAt := time.Date(2026, 8, 26, 12, 0, 0, 123, time.UTC)
	seedResourceReads(t, database, requestedAt)
	if _, err := database.ExecContext(ctx, "UPDATE entities SET enabled = 0 WHERE id = ?", readEntityC); err != nil {
		t.Fatal(err)
	}

	devicesPage, err := repository.ListDevices(ctx, ListDevicesParams{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(devicesPage.Items) != 1 || devicesPage.Items[0].ID != readDeviceA || !devicesPage.HasMore {
		t.Fatalf("first device page = %#v", devicesPage)
	}
	devicesPage, err = repository.ListDevices(ctx, ListDevicesParams{AfterID: new(readDeviceA), Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(devicesPage.Items) != 1 || devicesPage.Items[0].ID != readDeviceB || devicesPage.HasMore {
		t.Fatalf("second device page = %#v", devicesPage)
	}

	entitiesPage, err := repository.ListEntities(ctx, ListEntitiesParams{DeviceID: new(readDeviceA), Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(entitiesPage.Items) != 1 || entitiesPage.Items[0].Entity.ID != readEntityA ||
		entitiesPage.Items[0].State == nil ||
		string(entitiesPage.Items[0].State.Value) != "true" ||
		!entitiesPage.HasMore {
		t.Fatalf("filtered entities = %#v", entitiesPage)
	}
	unknownDevice := DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ff")
	empty, err := repository.ListEntities(ctx, ListEntitiesParams{DeviceID: &unknownDevice, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Items == nil || len(empty.Items) != 0 || empty.HasMore {
		t.Fatalf("unknown device entities = %#v", empty)
	}

	aggregate, err := repository.GetDevice(ctx, GetDeviceParams{ID: readDeviceA, EntityLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Device.Name != "Alpha" || len(aggregate.Entities.Items) != 1 ||
		aggregate.Entities.Items[0].Entity.ID != readEntityA || !aggregate.Entities.HasMore {
		t.Fatalf("first device aggregate page = %#v", aggregate)
	}
	aggregate, err = repository.GetDevice(ctx, GetDeviceParams{
		ID: readDeviceA, AfterEntityID: new(readEntityA), EntityLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.Entities.Items) != 1 || aggregate.Entities.Items[0].Entity.ID != readEntityC ||
		aggregate.Entities.Items[0].Entity.Enabled || aggregate.Entities.HasMore {
		t.Fatalf("second device aggregate page = %#v", aggregate)
	}
	if _, getErr := repository.GetDevice(
		ctx,
		GetDeviceParams{ID: unknownDevice, EntityLimit: 50},
	); !errors.Is(
		getErr,
		ErrDeviceNotFound,
	) {
		t.Fatalf("unknown device error = %v", getErr)
	}

	history, err := repository.ListEntityCommands(ctx, ListEntityCommandsParams{EntityID: readEntityA, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 1 || history.Items[0].ID != readCommandB || !history.HasMore {
		t.Fatalf("first command page = %#v", history)
	}
	history, err = repository.ListEntityCommands(ctx, ListEntityCommandsParams{
		EntityID: readEntityA, BeforeRequestedAt: &requestedAt, BeforeID: new(readCommandB), Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 1 || history.Items[0].ID != readCommandA || history.HasMore {
		t.Fatalf("second command page = %#v", history)
	}
}

func TestSQLiteCommandHistoryOrdersWholeAndFractionalSecondsChronologically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openMigratedDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	repository := NewSQLiteRepository(database, nil)
	exactSecond := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	seedResourceReads(t, database, exactSecond)
	if _, err := database.ExecContext(ctx, "DELETE FROM commands"); err != nil {
		t.Fatal(err)
	}

	earlier := newCommandRecord(t, readEntityA, exactSecond)
	later := newCommandRecord(t, readEntityA, exactSecond.Add(100*time.Millisecond))
	for _, command := range []CommandRecord{earlier, later} {
		if _, err := repository.CreateCommand(ctx, command); err != nil {
			t.Fatal(err)
		}
	}

	first, err := repository.ListEntityCommands(ctx, ListEntityCommandsParams{EntityID: readEntityA, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != later.ID || !first.HasMore {
		t.Fatalf("first command page = %#v", first)
	}
	second, err := repository.ListEntityCommands(ctx, ListEntityCommandsParams{
		EntityID: readEntityA, BeforeRequestedAt: &later.RequestedAt, BeforeID: &later.ID, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != earlier.ID || second.HasMore {
		t.Fatalf("second command page = %#v", second)
	}

	var stored string
	if scanErr := database.QueryRowContext(ctx, "SELECT requested_at FROM commands WHERE id = ?", earlier.ID).
		Scan(&stored); scanErr != nil {
		t.Fatal(scanErr)
	}
	if stored != "2026-08-26T12:00:00.000000000Z" {
		t.Fatalf("stored requested_at = %q", stored)
	}
}

func seedResourceReads(t *testing.T, database *sql.DB, requestedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	timestamp := formatTime(requestedAt)
	for _, device := range []struct {
		id   DeviceID
		name string
	}{{readDeviceA, "Alpha"}, {readDeviceB, "Beta"}} {
		if _, err := database.ExecContext(ctx, `
			INSERT INTO devices (id, kind, name, created_at, updated_at)
			VALUES (?, 'light', ?, ?, ?)`, device.id, device.name, timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
		bindingKey := "binding-" + device.name
		if _, err := database.ExecContext(ctx, `
			INSERT INTO adapter_bindings (adapter_id, binding_key, device_id, created_at, updated_at)
			VALUES ('simulator', ?, ?, ?, ?)`, bindingKey, device.id, timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
	}
	for _, entity := range []struct {
		id       EntityID
		deviceID DeviceID
		key      string
	}{{readEntityA, readDeviceA, "power-a"}, {readEntityB, readDeviceB, "power-b"}, {readEntityC, readDeviceA, "power-c"}} {
		if _, err := database.ExecContext(ctx, `
			INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at)
			VALUES (?, ?, 'Power', 'hearth.power/v1', '{"state":{},"operations":{"set":{}}}', ?, ?)`,
			entity.id, entity.deviceID, timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
		bindingKey := "binding-Alpha"
		if entity.deviceID == readDeviceB {
			bindingKey = "binding-Beta"
		}
		if _, err := database.ExecContext(ctx, `
			INSERT INTO adapter_entity_mappings (
				adapter_id, binding_key, entity_key, entity_id, external_entity_id, created_at, updated_at
			) VALUES ('simulator', ?, ?, ?, ?, ?, ?)`,
			bindingKey, entity.key, entity.id, "external-"+entity.key, timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
	}
	observationID := "obs_01890f47-7a6b-7c4d-8e9f-0123456789a1"
	if _, err := database.ExecContext(ctx, `
		INSERT INTO observation_receipts (
			observation_id, adapter_id, entity_id, disposition,
			adapter_received_at, observed_at, expires_at
		) VALUES (?, 'simulator', ?, 'applied', ?, ?, ?)`,
		observationID, readEntityA, timestamp, timestamp, formatTime(requestedAt.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO entity_states (
			entity_id, observation_id, value_json, adapter_received_at, observed_at, receive_order
		) SELECT ?, ?, 'true', ?, ?, receive_order
		  FROM observation_receipts WHERE observation_id = ?`,
		readEntityA, observationID, timestamp, timestamp, observationID); err != nil {
		t.Fatal(err)
	}
	for _, commandID := range []CommandID{readCommandA, readCommandB} {
		if _, err := database.ExecContext(ctx, `
			INSERT INTO commands (
				id, entity_id, adapter_id, operation, parameters_json, correlation_id,
				status, requested_at, deadline_at
			) VALUES (?, ?, 'simulator', 'set', '{"value":true}',
				'cor_01890f47-7a6b-7c4d-8e9f-0123456789ab', 'requested', ?, ?)`,
			commandID, readEntityA, timestamp, formatTime(requestedAt.Add(10*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
}
