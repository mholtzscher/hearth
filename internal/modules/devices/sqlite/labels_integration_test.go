//go:build integration

package sqlite //nolint:testpackage // Persistence contract tests share registration fixtures.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type labelFixture struct {
	database   *sql.DB
	repository *DeviceRepository
	service    *devices.Service
	binding    devices.Binding
	now        *time.Time
}

func newLabelFixture(t *testing.T) labelFixture {
	t.Helper()
	ctx := context.Background()
	database := openRegistrationDatabase(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog := firstLightCatalog(t)
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	repository := NewDeviceRepository(database, catalog)
	fixture := labelFixture{database: database, repository: repository, now: &now}
	fixture.service = newTestService(repository, nil, catalog, devices.Dependencies{
		Now: func() time.Time { return *fixture.now },
	})
	binding, err := fixture.service.Register(ctx, "simulator", testRuntimeID, multiEntityRegistration())
	if err != nil {
		t.Fatal(err)
	}
	fixture.binding = binding
	return fixture
}

func (fixture labelFixture) create(t *testing.T, name string, kind devices.LabelKind) devices.Label {
	t.Helper()
	label, err := fixture.service.CreateLabel(context.Background(), devices.CreateLabelParams{Name: name, Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	return label
}

func (fixture labelFixture) updatedAt(t *testing.T, table, id string) string {
	t.Helper()
	var value string
	if err := fixture.database.QueryRow("SELECT updated_at FROM "+table+" WHERE id = ?", id).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestLabelCatalogNormalizesNamesAndEnforcesUniquenessAndBound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newLabelFixture(t)
	kitchen, err := fixture.service.CreateLabel(ctx, devices.CreateLabelParams{
		Name: " Kitchen ", Kind: devices.LabelKindArea,
	})
	if err != nil || kitchen.Name != "Kitchen" || kitchen.Kind != devices.LabelKindArea ||
		!kitchen.CreatedAt.Equal(*fixture.now) {
		t.Fatalf("created = %#v, %v", kitchen, err)
	}
	for _, kind := range []devices.LabelKind{devices.LabelKindArea, devices.LabelKindTag} {
		if _, err = fixture.service.CreateLabel(
			ctx,
			devices.CreateLabelParams{Name: "KITCHEN", Kind: kind},
		); !errors.Is(
			err,
			devices.ErrLabelNameConflict,
		) {
			t.Fatalf("duplicate %s name = %v", kind, err)
		}
	}
	for _, params := range []devices.CreateLabelParams{
		{Name: "", Kind: devices.LabelKindTag},
		{Name: "\tBad", Kind: devices.LabelKindTag},
		{Name: "Room", Kind: "floor"},
	} {
		if _, err = fixture.service.CreateLabel(ctx, params); !errors.Is(err, devices.ErrInvalidLabel) {
			t.Fatalf("invalid %#v = %v", params, err)
		}
	}
	bedtime := fixture.create(t, "Bedtime", devices.LabelKindTag)

	*fixture.now = fixture.now.Add(time.Minute)
	pinned := fixture.updatedAt(t, "labels", string(bedtime.ID))
	if same, renameErr := fixture.service.RenameLabel(ctx, bedtime.ID, " Bedtime "); renameErr != nil ||
		fixture.updatedAt(t, "labels", string(bedtime.ID)) != pinned || same.Name != "Bedtime" {
		t.Fatalf("same-name rename = %#v, %v", same, renameErr)
	}
	if cased, renameErr := fixture.service.RenameLabel(ctx, bedtime.ID, "bedtime"); renameErr != nil ||
		cased.Name != "bedtime" || !cased.UpdatedAt.Equal(*fixture.now) {
		t.Fatalf("case-only rename = %#v, %v", cased, renameErr)
	}
	if _, err = fixture.service.RenameLabel(ctx, bedtime.ID, "kitchen"); !errors.Is(err, devices.ErrLabelNameConflict) {
		t.Fatalf("conflicting rename = %v", err)
	}
	missing := devices.LabelID("lbl_01890f47-7a6b-7c4d-8e9f-0123456789ff")
	if _, err = fixture.service.RenameLabel(ctx, missing, "Gone"); !errors.Is(err, devices.ErrLabelNotFound) {
		t.Fatalf("rename missing = %v", err)
	}
	if err = fixture.service.DeleteLabel(ctx, missing); !errors.Is(err, devices.ErrLabelNotFound) {
		t.Fatalf("delete missing = %v", err)
	}

	listed, err := fixture.service.ListLabels(ctx)
	if err != nil || len(listed) != 2 || listed[0].ID != kitchen.ID || listed[1].ID != bedtime.ID {
		t.Fatalf("list = %#v, %v", listed, err)
	}
	for index := len(listed); index < devices.MaxLabels; index++ {
		fixture.create(t, fmt.Sprintf("Tag %03d", index), devices.LabelKindTag)
	}
	if _, err = fixture.service.CreateLabel(ctx, devices.CreateLabelParams{
		Name: "One too many", Kind: devices.LabelKindTag,
	}); !errors.Is(err, devices.ErrLabelLimitReached) {
		t.Fatalf("label past bound = %v", err)
	}
}

//nolint:gocognit // One fixture checks the same effective Labels across every read path.
func TestLabelAssignmentsDeriveEffectiveLabelsOnEveryRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newLabelFixture(t)
	kitchen := fixture.create(t, "Kitchen", devices.LabelKindArea)
	pantry := fixture.create(t, "Pantry", devices.LabelKindArea)
	bedtime := fixture.create(t, "Bedtime", devices.LabelKindTag)
	away := fixture.create(t, "Away", devices.LabelKindTag)
	deviceID := fixture.binding.DeviceID
	power, brightness := fixture.binding.Entities[0].EntityID, fixture.binding.Entities[1].EntityID

	device, err := fixture.service.PatchDevice(ctx, deviceID, devices.DevicePatch{
		AreaEdit: &devices.AreaEdit{AreaID: new(kitchen.ID)},
		TagEdit:  &devices.TagEdit{TagIDs: []devices.LabelID{bedtime.ID, bedtime.ID}},
	})
	wantDevice := devices.DeviceLabels{
		Area: &devices.LabelRef{ID: kitchen.ID, Name: "Kitchen"},
		Tags: []devices.LabelRef{{ID: bedtime.ID, Name: "Bedtime"}},
	}
	if err != nil || !reflect.DeepEqual(device.Labels, wantDevice) {
		t.Fatalf("device patch labels = %#v, %v", device.Labels, err)
	}
	view, err := fixture.service.PatchEntity(ctx, brightness, devices.EntityPatch{
		AreaEdit: &devices.AreaEdit{AreaID: new(pantry.ID)},
		TagEdit:  &devices.TagEdit{TagIDs: []devices.LabelID{away.ID, bedtime.ID}},
	})
	wantOverride := devices.EntityLabels{
		Area: &devices.EffectiveArea{
			Label:  devices.LabelRef{ID: pantry.ID, Name: "Pantry"},
			Source: devices.AreaSourceEntity,
		},
		Tags: []devices.LabelRef{{ID: away.ID, Name: "Away"}, {ID: bedtime.ID, Name: "Bedtime"}},
	}
	if err != nil || !reflect.DeepEqual(view.Labels, wantOverride) {
		t.Fatalf("entity patch labels = %#v, %v", view.Labels, err)
	}
	wantInherited := devices.EntityLabels{
		Area: &devices.EffectiveArea{
			Label:  devices.LabelRef{ID: kitchen.ID, Name: "Kitchen"},
			Source: devices.AreaSourceDevice,
		},
		Tags: []devices.LabelRef{{ID: bedtime.ID, Name: "Bedtime"}},
	}

	reads := map[string]func() (devices.EntityLabels, error){
		"get entity": func() (devices.EntityLabels, error) {
			got, readErr := fixture.service.GetEntity(ctx, power)
			return got.Labels, readErr
		},
		"list entities": func() (devices.EntityLabels, error) {
			page, readErr := fixture.service.ListEntities(ctx, devices.ListEntitiesParams{Limit: 10})
			if readErr != nil || len(page.Items) != 2 {
				return devices.EntityLabels{}, fmt.Errorf("page %#v: %w", page, readErr)
			}
			for _, item := range page.Items {
				if item.Entity.ID == brightness && !reflect.DeepEqual(item.Labels, wantOverride) {
					return devices.EntityLabels{}, fmt.Errorf("override entity labels %#v", item.Labels)
				}
			}
			return labelsOf(page.Items, power), nil
		},
		"device entities": func() (devices.EntityLabels, error) {
			aggregate, readErr := fixture.service.GetDevice(ctx, devices.GetDeviceParams{ID: deviceID, EntityLimit: 10})
			if readErr != nil || !reflect.DeepEqual(aggregate.Device.Labels, wantDevice) {
				return devices.EntityLabels{}, fmt.Errorf("device %#v: %w", aggregate.Device.Labels, readErr)
			}
			return labelsOf(aggregate.Entities.Items, power), nil
		},
		"enablement": func() (devices.EntityLabels, error) {
			enabled := false
			got, readErr := fixture.service.PatchEntity(ctx, power, devices.EntityPatch{Enabled: &enabled})
			return got.Labels, readErr
		},
	}
	for name, read := range reads {
		if got, readErr := read(); readErr != nil || !reflect.DeepEqual(got, wantInherited) {
			t.Fatalf("%s labels = %#v, %v", name, got, readErr)
		}
	}
	listed, err := fixture.service.ListDevices(ctx, devices.ListDevicesParams{Limit: 10})
	if err != nil || len(listed.Items) != 1 || !reflect.DeepEqual(listed.Items[0].Labels, wantDevice) {
		t.Fatalf("list devices = %#v, %v", listed, err)
	}

	// Clearing the Entity Area returns it to its Device's Area.
	view, err = fixture.service.PatchEntity(ctx, brightness, devices.EntityPatch{AreaEdit: &devices.AreaEdit{}})
	if err != nil || view.Labels.Area == nil || view.Labels.Area.Source != devices.AreaSourceDevice {
		t.Fatalf("inherited area = %#v, %v", view.Labels.Area, err)
	}
	// Deleting a Label clears it everywhere without removing the Device.
	if err = fixture.service.DeleteLabel(ctx, kitchen.ID); err != nil {
		t.Fatal(err)
	}
	if err = fixture.service.DeleteLabel(ctx, bedtime.ID); err != nil {
		t.Fatal(err)
	}
	view, err = fixture.service.GetEntity(ctx, brightness)
	if err != nil || view.Labels.Area != nil ||
		!reflect.DeepEqual(view.Labels.Tags, []devices.LabelRef{{ID: away.ID, Name: "Away"}}) {
		t.Fatalf("labels after delete = %#v, %v", view.Labels, err)
	}
}

func labelsOf(items []devices.EntityWithState, id devices.EntityID) devices.EntityLabels {
	for _, item := range items {
		if item.Entity.ID == id {
			return item.Labels
		}
	}
	return devices.EntityLabels{}
}

func TestLabelPatchesRejectBadLabelsAtomicallyAndKeepTimestampsAndFacts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newLabelFixture(t)
	kitchen := fixture.create(t, "Kitchen", devices.LabelKindArea)
	bedtime := fixture.create(t, "Bedtime", devices.LabelKindTag)
	power := fixture.binding.Entities[0].EntityID
	missing := devices.LabelID("lbl_01890f47-7a6b-7c4d-8e9f-0123456789ff")
	initialFacts, err := fixture.repository.ListPendingDeviceFacts(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	for label, patch := range map[string]devices.EntityPatch{
		"unknown area": {NameEdit: &devices.NameEdit{Override: &name}, AreaEdit: &devices.AreaEdit{AreaID: new(missing)}},
		"tag as area":  {NameEdit: &devices.NameEdit{Override: &name}, AreaEdit: &devices.AreaEdit{AreaID: new(bedtime.ID)}},
		"area as tag":  {NameEdit: &devices.NameEdit{Override: &name}, TagEdit: &devices.TagEdit{TagIDs: []devices.LabelID{kitchen.ID}}},
	} {
		if _, err = fixture.service.PatchEntity(ctx, power, patch); !errors.Is(err, devices.ErrInvalidMetadataPatch) {
			t.Fatalf("%s = %v", label, err)
		}
	}
	if _, err = fixture.service.PatchEntity(ctx, power, devices.EntityPatch{
		TagEdit: &devices.TagEdit{TagIDs: []devices.LabelID{missing}},
	}); !errors.Is(err, devices.ErrLabelNotFound) {
		t.Fatalf("unknown tag = %v", err)
	}
	view, err := fixture.service.GetEntity(ctx, power)
	if err != nil || view.Entity.NameOverride != nil || view.Labels.Area != nil || len(view.Labels.Tags) != 0 {
		t.Fatalf("rejected patches wrote %#v, %v", view, err)
	}

	*fixture.now = fixture.now.Add(time.Minute)
	patch := devices.EntityPatch{
		AreaEdit: &devices.AreaEdit{AreaID: new(kitchen.ID)},
		TagEdit:  &devices.TagEdit{TagIDs: []devices.LabelID{bedtime.ID}},
	}
	if _, err = fixture.service.PatchEntity(ctx, power, patch); err != nil {
		t.Fatal(err)
	}
	pinned := fixture.updatedAt(t, "entities", string(power))
	*fixture.now = fixture.now.Add(time.Minute)
	if _, err = fixture.service.PatchEntity(ctx, power, patch); err != nil {
		t.Fatal(err)
	}
	if after := fixture.updatedAt(t, "entities", string(power)); after != pinned {
		t.Fatalf("unchanged label edit moved updated_at %q -> %q", pinned, after)
	}
	devicePatch := devices.DevicePatch{TagEdit: &devices.TagEdit{TagIDs: []devices.LabelID{bedtime.ID}}}
	if _, err = fixture.service.PatchDevice(ctx, fixture.binding.DeviceID, devicePatch); err != nil {
		t.Fatal(err)
	}
	pinned = fixture.updatedAt(t, "devices", string(fixture.binding.DeviceID))
	*fixture.now = fixture.now.Add(time.Minute)
	if _, err = fixture.service.PatchDevice(ctx, fixture.binding.DeviceID, devicePatch); err != nil {
		t.Fatal(err)
	}
	if after := fixture.updatedAt(t, "devices", string(fixture.binding.DeviceID)); after != pinned {
		t.Fatalf("unchanged device tags moved updated_at %q -> %q", pinned, after)
	}
	facts, err := fixture.repository.ListPendingDeviceFacts(ctx, 100)
	if err != nil || !reflect.DeepEqual(facts, initialFacts) {
		t.Fatalf("label edits emitted Device Facts: %#v, %v", facts, err)
	}
}

func TestEntitySelectorMatchesEveryLabelAndAnyType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newLabelFixture(t)
	kitchen := fixture.create(t, "Kitchen", devices.LabelKindArea)
	bedtime := fixture.create(t, "Bedtime", devices.LabelKindTag)
	unused := fixture.create(t, "Unused", devices.LabelKindTag)
	deviceID := fixture.binding.DeviceID
	power, brightness := fixture.binding.Entities[0].EntityID, fixture.binding.Entities[1].EntityID
	if _, err := fixture.service.PatchDevice(ctx, deviceID, devices.DevicePatch{
		AreaEdit: &devices.AreaEdit{AreaID: new(kitchen.ID)},
	}); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := fixture.service.PatchEntity(ctx, power, devices.EntityPatch{
		Enabled: &disabled, TagEdit: &devices.TagEdit{TagIDs: []devices.LabelID{bedtime.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	list := func(selector devices.EntitySelector, device *devices.DeviceID) ([]devices.EntityID, error) {
		page, err := fixture.service.ListEntities(ctx, devices.ListEntitiesParams{
			DeviceID: device, Selector: selector, Limit: 10,
		})
		ids := make([]devices.EntityID, len(page.Items))
		for index, item := range page.Items {
			ids[index] = item.Entity.ID
		}
		return ids, err
	}
	otherDevice := devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ff")
	both := []devices.EntityID{power, brightness}
	if power > brightness {
		both = []devices.EntityID{brightness, power}
	}
	for name, test := range map[string]struct {
		selector devices.EntitySelector
		device   *devices.DeviceID
		want     []devices.EntityID
	}{
		"inherited area includes disabled": {selector: devices.EntitySelector{Labels: []devices.LabelID{kitchen.ID}}, want: both},
		"every label":                      {selector: devices.EntitySelector{Labels: []devices.LabelID{bedtime.ID, kitchen.ID}}, want: []devices.EntityID{power}},
		"label nobody has":                 {selector: devices.EntitySelector{Labels: []devices.LabelID{kitchen.ID, unused.ID}}, want: []devices.EntityID{}},
		"type":                             {selector: devices.EntitySelector{Types: []devices.EntityTypeID{devices.EntityTypeBrightnessV1}}, want: []devices.EntityID{brightness}},
		"label and type":                   {selector: devices.EntitySelector{Labels: []devices.LabelID{kitchen.ID}, Types: []devices.EntityTypeID{devices.EntityTypePowerV1}}, want: []devices.EntityID{power}},
		"with device":                      {selector: devices.EntitySelector{Labels: []devices.LabelID{kitchen.ID}}, device: &deviceID, want: both},
		"with other device":                {selector: devices.EntitySelector{Labels: []devices.LabelID{kitchen.ID}}, device: &otherDevice, want: []devices.EntityID{}},
	} {
		got, err := list(test.selector, test.device)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%s = %v, %v; want %v", name, got, err, test.want)
		}
	}
	missing := devices.LabelID("lbl_01890f47-7a6b-7c4d-8e9f-0123456789ff")
	if _, err := list(
		devices.EntitySelector{Labels: []devices.LabelID{missing}},
		nil,
	); !errors.Is(
		err,
		devices.ErrLabelNotFound,
	) {
		t.Fatalf("unknown selector label = %v", err)
	}
	for name, selector := range map[string]devices.EntitySelector{
		"bad label ID": {Labels: []devices.LabelID{"kitchen"}},
		"unknown type": {Types: []devices.EntityTypeID{"hearth.unknown/v1"}},
	} {
		if _, err := list(selector, nil); !errors.Is(err, devices.ErrInvalidEntitySelector) {
			t.Fatalf("%s = %v", name, err)
		}
		if _, err := fixture.repository.ListEntities(ctx, devices.ListEntitiesParams{
			Selector: selector, Limit: 10,
		}); !errors.Is(err, devices.ErrInvalidEntitySelector) {
			t.Fatalf("repository %s = %v", name, err)
		}
	}
	page, err := fixture.service.ListEntities(ctx, devices.ListEntitiesParams{
		Selector: devices.EntitySelector{Labels: []devices.LabelID{kitchen.ID}}, Limit: 1,
	})
	if err != nil || len(page.Items) != 1 || !page.HasMore {
		t.Fatalf("first selector page = %#v, %v", page, err)
	}
	after := page.Items[0].Entity.ID
	page, err = fixture.service.ListEntities(ctx, devices.ListEntitiesParams{
		Selector: devices.EntitySelector{Labels: []devices.LabelID{kitchen.ID}}, AfterID: &after, Limit: 1,
	})
	if err != nil || len(page.Items) != 1 || page.HasMore || page.Items[0].Entity.ID == after {
		t.Fatalf("second selector page = %#v, %v", page, err)
	}
}
