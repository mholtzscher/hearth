package sqlite //nolint:testpackage // Persistence contract tests share registration fixtures.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestDeviceMetadataBoundaryAndNoopTimestamps(t *testing.T) {
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
	text := "\u2003Kitchen  ceiling\u2003"
	params := devices.PatchDeviceParams{
		DeviceID:  binding.DeviceID,
		UpdatedAt: now.Add(time.Minute),
		Patch:     devices.DevicePatch{NameEdit: &devices.NameEdit{Override: &text}},
	}
	device, err := repository.PatchDevice(ctx, params)
	if err != nil || device.Name != "Kitchen  ceiling" || device.NameOverride == nil {
		t.Fatalf("Device normalization = %#v, %v", device, err)
	}
	var pinnedAt string
	if err = database.QueryRow("SELECT updated_at FROM devices WHERE id = ?", binding.DeviceID).
		Scan(&pinnedAt); err != nil {
		t.Fatal(err)
	}
	params.UpdatedAt = now.Add(2 * time.Minute)
	if _, err = repository.PatchDevice(ctx, params); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", "\tKitchen", string([]byte{0xff})} {
		params.Patch.NameEdit.Override = &invalid
		if _, err = repository.PatchDevice(ctx, params); !errors.Is(err, devices.ErrInvalidMetadataPatch) {
			t.Fatalf("invalid Device name = %v", err)
		}
	}
	var afterAt string
	if err = database.QueryRow("SELECT updated_at FROM devices WHERE id = ?", binding.DeviceID).
		Scan(&afterAt); err != nil {
		t.Fatal(err)
	}
	if pinnedAt != afterAt {
		t.Fatalf("no-op/invalid edits changed timestamp: %q, %q", pinnedAt, afterAt)
	}
	params.Patch = devices.DevicePatch{}
	if _, err = repository.PatchDevice(ctx, params); !errors.Is(err, devices.ErrInvalidMetadataPatch) {
		t.Fatalf("empty Device patch = %v", err)
	}
	params.Patch.NameEdit = &devices.NameEdit{}
	params.DeviceID = devices.DeviceID("bad")
	if _, err = repository.PatchDevice(ctx, params); !errors.Is(err, devices.ErrInvalidMetadataPatch) {
		t.Fatalf("invalid Device ID = %v", err)
	}
}
