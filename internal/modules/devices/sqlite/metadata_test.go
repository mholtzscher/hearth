package sqlite //nolint:testpackage // Real persistence tests share registration fixtures.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//nolint:gocognit // Ordered lifecycle scenario protects persistence across registration and reopen.
func TestMetadataPersistsPinsAndResetsAcrossRegistrationAndReopen(
	t *testing.T,
) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hearth.db")
	database := openRegistrationDatabase(t, path)
	catalog := firstLightCatalog(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return now }})
	registration := validDomainRegistration()
	binding, err := service.Register(ctx, "simulator", testRuntimeID, registration)
	if err != nil {
		t.Fatal(err)
	}
	id := binding.Entities[0].EntityID
	initial, err := repository.GetEntity(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	name := initial.Entity.Name
	deviceName := registration.Device.Name
	now = now.Add(time.Minute)
	if _, err = service.PatchDevice(
		ctx,
		binding.DeviceID,
		devices.DevicePatch{NameEdit: &devices.NameEdit{Override: &deviceName}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = service.PatchEntity(
		ctx,
		id,
		devices.EntityPatch{NameEdit: &devices.NameEdit{Override: &name}},
	); err != nil {
		t.Fatal(err)
	}
	assertEntityNameNoop(t, repository, id, name, now.Add(time.Minute), initial.Availability.Since)
	now = now.Add(time.Minute)
	registration.Device.Name = "New Adapter Device"
	registration.Entities[0].Name = "New Adapter Entity"
	if _, err = service.Register(ctx, "simulator", testRuntimeID, registration); err != nil {
		t.Fatal(err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	database = openMigratedDatabase(t, path)
	repository = NewDeviceRepository(database, catalog)
	view, err := repository.GetEntity(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Entity.Name != name || view.Entity.AdapterName != "New Adapter Entity" || view.Entity.NameOverride == nil ||
		*view.Entity.NameOverride != name {
		t.Fatalf("reopened Entity = %#v", view.Entity)
	}
	aggregate, err := repository.GetDevice(ctx, devices.GetDeviceParams{ID: binding.DeviceID, EntityLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Device.Name != deviceName || aggregate.Device.AdapterName != "New Adapter Device" ||
		aggregate.Device.NameOverride == nil {
		t.Fatalf("reopened Device = %#v", aggregate.Device)
	}
	// Later accepted edits need no precondition, even when based on an old read.
	assertSuccessiveEntityNames(t, repository, id, now)
	now = now.Add(2 * time.Minute)
	view, err = repository.SetEntityEnabled(
		ctx,
		devices.SetEntityEnabledParams{EntityID: id, Enabled: false, UpdatedAt: now.Add(time.Minute)},
	)
	if err != nil || view.Entity.Name != "Later household edit" {
		t.Fatalf("enablement preserves override = %#v, %v", view, err)
	}
	if err = repository.ReleaseAdapterRuntime(
		ctx,
		devices.ReleaseRuntimeWrite{AdapterID: "simulator", RuntimeID: testRuntimeID, ReleasedAt: now.Add(time.Minute)},
	); err != nil {
		t.Fatal(err)
	}
	before := view
	offlineName := "Offline renamed Entity"
	view, err = repository.PatchEntity(
		ctx,
		devices.PatchEntityParams{
			EntityID:  id,
			UpdatedAt: now.Add(2 * time.Minute),
			Patch:     devices.EntityPatch{NameEdit: &devices.NameEdit{Override: &offlineName}},
		},
	)
	if err != nil || view.Entity.Enabled || view.Entity.Name != offlineName {
		t.Fatalf("offline disabled rename = %#v, %v", view, err)
	}
	view, err = repository.PatchEntity(
		ctx,
		devices.PatchEntityParams{
			EntityID:  id,
			UpdatedAt: now.Add(2 * time.Minute),
			Patch:     devices.EntityPatch{NameEdit: &devices.NameEdit{}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if view.Entity.Enabled || view.Entity.Name != "New Adapter Entity" || view.Entity.NameOverride != nil ||
		!reflect.DeepEqual(view.State, before.State) {
		t.Fatalf("offline disabled reset = %#v", view)
	}
	device, err := repository.PatchDevice(
		ctx,
		devices.PatchDeviceParams{
			DeviceID:  binding.DeviceID,
			UpdatedAt: now,
			Patch:     devices.DevicePatch{NameEdit: &devices.NameEdit{}},
		},
	)
	if err != nil || device.Name != "New Adapter Device" || device.NameOverride != nil {
		t.Fatalf("Device reset = %#v, %v", device, err)
	}
}

func assertSuccessiveEntityNames(t *testing.T, repository *DeviceRepository, id devices.EntityID, now time.Time) {
	t.Helper()
	for _, text := range []string{"First household edit", "Later household edit"} {
		now = now.Add(time.Minute)
		view, err := repository.PatchEntity(
			context.Background(),
			devices.PatchEntityParams{
				EntityID:  id,
				UpdatedAt: now,
				Patch:     devices.EntityPatch{NameEdit: &devices.NameEdit{Override: &text}},
			},
		)
		if err != nil || view.Entity.Name != text {
			t.Fatalf("successive edit = %#v, %v", view, err)
		}
	}
}

func assertEntityNameNoop(
	t *testing.T,
	repository *DeviceRepository,
	id devices.EntityID,
	name string,
	now, registeredAt time.Time,
) {
	t.Helper()
	var pinnedAt string
	if err := repository.database.QueryRow("SELECT updated_at FROM entities WHERE id = ?", id).
		Scan(&pinnedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PatchEntity(
		context.Background(),
		devices.PatchEntityParams{
			EntityID:  id,
			UpdatedAt: now,
			Patch:     devices.EntityPatch{NameEdit: &devices.NameEdit{Override: &name}},
		},
	); err != nil {
		t.Fatal(err)
	}
	var noopAt string
	if err := repository.database.QueryRow("SELECT updated_at FROM entities WHERE id = ?", id).
		Scan(&noopAt); err != nil {
		t.Fatal(err)
	}
	if pinnedAt == formatTime(registeredAt) || noopAt != pinnedAt {
		t.Fatalf("pin/noop timestamps = %q, %q", pinnedAt, noopAt)
	}
}

func TestMixedMetadataRollbackAndNoDeviceFacts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return now }})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	id := binding.Entities[0].EntityID
	automationRepository := automationssqlite.NewAutomationRepository(database, automations.Dependencies{})
	automation, err := automationRepository.CreateAutomation(ctx, automations.Definition{
		Name:    "Keep canonical references",
		Enabled: true,
		Triggers: []automations.Trigger{
			{ID: "power_changed", Body: automations.ObservationTrigger{
				EntityID:     id,
				Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
			}},
		},
		Steps: []automations.Step{
			{
				ID: "power_on",
				Body: automations.CommandStep{
					EntityID:      id,
					OperationName: devices.OperationNameSet,
					Parameters:    devices.CommandParameters(`{"value":true}`),
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ProjectObservation(
		ctx,
		"simulator",
		testRuntimeID,
		newObservation(t, id, `true`, now),
		now,
	); err != nil {
		t.Fatal(err)
	}
	initialFacts, err := repository.ListPendingDeviceFacts(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	before, err := repository.GetEntity(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var beforeAt string
	if err = database.QueryRow("SELECT updated_at FROM entities WHERE id = ?", id).Scan(&beforeAt); err != nil {
		t.Fatal(err)
	}
	disabled := false
	name := "Kitchen"
	patch := devices.EntityPatch{Enabled: &disabled, NameEdit: &devices.NameEdit{Override: &name}}
	if _, err = database.Exec(
		`CREATE TABLE failed_metadata_commit (device_id TEXT REFERENCES devices(id) DEFERRABLE INITIALLY DEFERRED)`,
	); err != nil {
		t.Fatal(err)
	}
	for _, trigger := range []string{
		`CREATE TRIGGER reject_name BEFORE UPDATE OF name_override ON entities BEGIN SELECT RAISE(ABORT, 'name write failed'); END`,
		`CREATE TRIGGER reject_name AFTER UPDATE OF name_override ON entities BEGIN INSERT INTO failed_metadata_commit VALUES ('missing-device'); END`,
	} {
		assertMixedMetadataFailure(t, repository, service, id, patch, trigger, before, beforeAt)
	}
	after, err := service.PatchEntity(ctx, id, patch)
	if err != nil || after.Entity.Enabled || after.Entity.Name != name {
		t.Fatalf("mixed patch = %#v, %v", after, err)
	}
	if !reflect.DeepEqual(before.State, after.State) || !reflect.DeepEqual(before.Availability, after.Availability) ||
		before.Entity.ID != after.Entity.ID ||
		before.Entity.DeviceID != after.Entity.DeviceID {
		t.Fatal("metadata patch changed State, availability or identity")
	}
	commands, err := repository.ListCommands(ctx, devices.ListCommandsParams{Limit: 100})
	if err != nil || len(commands.Items) != 0 {
		t.Fatalf("Commands = %#v, %v", commands, err)
	}
	facts, err := repository.ListPendingDeviceFacts(ctx, 100)
	if err != nil || !reflect.DeepEqual(facts, initialFacts) {
		t.Fatalf("Device Facts = %#v, %v", facts, err)
	}
	afterAutomation, err := automationRepository.GetAutomation(ctx, automation.ID)
	if err != nil || !reflect.DeepEqual(automation, afterAutomation) {
		t.Fatalf("metadata patch changed Automation references = %#v, %v", afterAutomation, err)
	}
}

func assertMixedMetadataFailure(
	t *testing.T,
	repository *DeviceRepository,
	service *devices.Service,
	id devices.EntityID,
	patch devices.EntityPatch,
	trigger string,
	before devices.EntityWithState,
	beforeAt string,
) {
	t.Helper()
	if _, err := repository.database.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PatchEntity(context.Background(), id, patch); err == nil {
		t.Fatal("mixed patch unexpectedly succeeded")
	}
	after, err := repository.GetEntity(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var afterAt string
	if err = repository.database.QueryRow("SELECT updated_at FROM entities WHERE id = ?", id).
		Scan(&afterAt); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || beforeAt != afterAt {
		t.Fatalf("failed mixed patch changed metadata: %#v", after)
	}
	if _, err = repository.database.Exec("DROP TRIGGER reject_name"); err != nil {
		t.Fatal(err)
	}
}

func TestDirectMetadataBoundaryNormalizesAndRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return now }})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	id := binding.Entities[0].EntityID
	for _, test := range []struct {
		value, want string
		invalid     bool
	}{
		{"\u2003 Kitchen  ceiling \u2003", "Kitchen  ceiling", false},
		{strings.Repeat("界", 128), strings.Repeat("界", 128), false},
		{strings.Repeat("界", 129), "", true},
		{"   ", "", true}, {"\tKitchen", "", true}, {"Kitchen\u007f", "", true}, {string([]byte{0xff}), "", true},
	} {
		before, readErr := repository.GetEntity(ctx, id)
		if readErr != nil {
			t.Fatal(readErr)
		}
		view, patchErr := repository.PatchEntity(
			ctx,
			devices.PatchEntityParams{
				EntityID:  id,
				UpdatedAt: now,
				Patch:     devices.EntityPatch{NameEdit: &devices.NameEdit{Override: &test.value}},
			},
		)
		if test.invalid {
			if !errors.Is(patchErr, devices.ErrInvalidMetadataPatch) {
				t.Fatalf("invalid %q: %v", test.value, patchErr)
			}
			after, afterErr := repository.GetEntity(ctx, id)
			if afterErr != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("invalid name mutated Entity")
			}
		} else if patchErr != nil || view.Entity.Name != test.want {
			t.Fatalf("name %q = %#v, %v", test.value, view, patchErr)
		}
	}
	if _, err = repository.PatchEntity(
		ctx,
		devices.PatchEntityParams{EntityID: id, UpdatedAt: now},
	); !errors.Is(
		err,
		devices.ErrInvalidMetadataPatch,
	) {
		t.Fatalf("empty patch = %v", err)
	}
}

func TestNameOverrideMigrationDownUpPreservesAdapterMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	repository := NewDeviceRepository(database, catalog)
	service := newTestService(repository, nil, catalog, devices.Dependencies{Now: func() time.Time { return now }})
	binding, err := service.Register(ctx, "simulator", testRuntimeID, validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	id := binding.Entities[0].EntityID
	if _, err = service.ProjectObservation(
		ctx,
		"simulator",
		testRuntimeID,
		newObservation(t, id, `true`, now),
		now,
	); err != nil {
		t.Fatal(err)
	}
	before, err := repository.GetEntity(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, os.DirFS("../../../platform/db/migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := repository.GetEntity(ctx, id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("populated v7 upgrade = %#v, %v", after, err)
	}
	name := "Household override"
	if _, err = service.PatchEntity(
		ctx,
		id,
		devices.EntityPatch{NameEdit: &devices.NameEdit{Override: &name}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = service.PatchDevice(
		ctx,
		binding.DeviceID,
		devices.DevicePatch{NameEdit: &devices.NameEdit{Override: &name}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	after, err = repository.GetEntity(ctx, id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("downgrade loss policy = %#v, %v", after, err)
	}
	aggregate, err := repository.GetDevice(ctx, devices.GetDeviceParams{ID: binding.DeviceID, EntityLimit: 100})
	if err != nil || aggregate.Device.NameOverride != nil || aggregate.Device.Name != aggregate.Device.AdapterName {
		t.Fatalf("Device after downgrade = %#v, %v", aggregate, err)
	}
}
