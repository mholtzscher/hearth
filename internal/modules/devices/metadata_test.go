package devices //nolint:testpackage // Tests share canonical domain identities.

import (
	"context"
	"errors"
	"testing"
	"time"
)

type metadataCapture struct {
	device PatchDeviceParams
	entity PatchEntityParams
	name   string
}

func (capture *metadataCapture) PatchDevice(_ context.Context, params PatchDeviceParams) (Device, error) {
	capture.device = params
	return Device{NameOverride: &capture.name}, nil
}

func (capture *metadataCapture) PatchEntity(_ context.Context, params PatchEntityParams) (EntityWithState, error) {
	capture.entity = params
	return EntityWithState{Entity: Entity{NameOverride: &capture.name}}, nil
}

func TestMetadataServicePreparesInputsAndOwnsReturnedNames(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	capture := &metadataCapture{name: "Pinned"}
	service := NewService(Stores{Metadata: capture}, nil, nil, Dependencies{Now: func() time.Time { return now }})
	deviceID := DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	entityID := EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	name := "\u2003Kitchen  light\u2003"
	disabled := false
	device, err := service.PatchDevice(ctx, deviceID, DevicePatch{NameEdit: &NameEdit{Override: &name}})
	if err != nil {
		t.Fatal(err)
	}
	entity, err := service.PatchEntity(
		ctx,
		entityID,
		EntityPatch{Enabled: &disabled, NameEdit: &NameEdit{Override: &name}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if capture.device.DeviceID != deviceID || capture.entity.EntityID != entityID ||
		!capture.device.UpdatedAt.Equal(now) ||
		!capture.entity.UpdatedAt.Equal(now) ||
		*capture.device.Patch.NameEdit.Override != "Kitchen  light" ||
		*capture.entity.Patch.NameEdit.Override != "Kitchen  light" ||
		*capture.entity.Patch.Enabled {
		t.Fatalf("prepared inputs = %#v, %#v", capture.device, capture.entity)
	}
	*device.NameOverride = "changed by caller"
	*entity.Entity.NameOverride = "changed by caller"
	name = "changed input"
	disabled = true
	if capture.name != "Pinned" || *capture.entity.Patch.Enabled ||
		*capture.entity.Patch.NameEdit.Override != "Kitchen  light" {
		t.Fatal("metadata boundary shares mutable pointers")
	}
}

func TestMetadataServiceRejectsInvalidInputsBeforePersistence(t *testing.T) {
	t.Parallel()
	// No repository is configured: any persistence call fails this test.
	service := NewService(Stores{}, nil, nil, Dependencies{})
	id := EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	blank := "  "
	for _, test := range []struct {
		id    EntityID
		patch EntityPatch
	}{
		{"bad", EntityPatch{NameEdit: &NameEdit{}}},
		{id, EntityPatch{}},
		{id, EntityPatch{NameEdit: &NameEdit{Override: &blank}}},
	} {
		if _, err := service.PatchEntity(
			context.Background(),
			test.id,
			test.patch,
		); !errors.Is(
			err,
			ErrInvalidMetadataPatch,
		) {
			t.Fatalf("invalid input = %v", err)
		}
	}
	if _, err := service.PatchDevice(
		context.Background(),
		DeviceID("bad"),
		DevicePatch{NameEdit: &NameEdit{}},
	); !errors.Is(
		err,
		ErrInvalidMetadataPatch,
	) {
		t.Fatalf("invalid Device = %v", err)
	}
}
