package devices

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type NameEdit struct{ Override *string }
type DevicePatch struct{ NameEdit *NameEdit }
type EntityPatch struct {
	Enabled  *bool
	NameEdit *NameEdit
}

func NormalizeNameOverride(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%w: name must be valid UTF-8", ErrInvalidMetadataPatch)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return "", fmt.Errorf("%w: name must not contain control characters", ErrInvalidMetadataPatch)
		}
	}
	value = strings.TrimSpace(value)
	if length := utf8.RuneCountInString(value); length < 1 || length > 128 {
		return "", fmt.Errorf("%w: name must contain 1–128 code points", ErrInvalidMetadataPatch)
	}
	return value, nil
}

// PrepareMetadataPatch validates freely constructed edits and returns owned,
// normalized values. Service and repository are independent input boundaries.
func PrepareMetadataPatch(enabled *bool, edit *NameEdit) (EntityPatch, error) {
	if enabled == nil && edit == nil {
		return EntityPatch{}, ErrInvalidMetadataPatch
	}
	patch := EntityPatch{}
	if enabled != nil {
		value := *enabled
		patch.Enabled = &value
	}
	if edit != nil {
		patch.NameEdit = &NameEdit{}
		if edit.Override != nil {
			value, err := NormalizeNameOverride(*edit.Override)
			if err != nil {
				return EntityPatch{}, err
			}
			patch.NameEdit.Override = &value
		}
	}
	return patch, nil
}

func (service *Service) PatchDevice(ctx context.Context, id DeviceID, patch DevicePatch) (Device, error) {
	if _, err := ParseDeviceID(string(id)); err != nil {
		return Device{}, fmt.Errorf("%w: %w", ErrInvalidMetadataPatch, err)
	}
	prepared, err := PrepareMetadataPatch(nil, patch.NameEdit)
	if err != nil {
		return Device{}, err
	}
	now := service.dependencies.Now().UTC()
	if now.IsZero() {
		return Device{}, fmt.Errorf("metadata clock returned zero time")
	}
	device, err := service.stores.Metadata.PatchDevice(
		ctx,
		PatchDeviceParams{DeviceID: id, Patch: DevicePatch{NameEdit: prepared.NameEdit}, UpdatedAt: now},
	)
	return CopyDevice(device), err
}

func (service *Service) PatchEntity(ctx context.Context, id EntityID, patch EntityPatch) (EntityWithState, error) {
	if _, err := ParseEntityID(string(id)); err != nil {
		return EntityWithState{}, fmt.Errorf("%w: %w", ErrInvalidMetadataPatch, err)
	}
	prepared, err := PrepareMetadataPatch(patch.Enabled, patch.NameEdit)
	if err != nil {
		return EntityWithState{}, err
	}
	now := service.dependencies.Now().UTC()
	if now.IsZero() {
		return EntityWithState{}, fmt.Errorf("metadata clock returned zero time")
	}
	view, err := service.stores.Metadata.PatchEntity(
		ctx,
		PatchEntityParams{EntityID: id, Patch: prepared, UpdatedAt: now},
	)
	return CopyEntityWithState(view), err
}

func CopyDevice(device Device) Device {
	if device.NameOverride != nil {
		value := *device.NameOverride
		device.NameOverride = &value
	}
	return device
}
