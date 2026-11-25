package devices

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type NameEdit struct{ Override *string }

// AreaEdit sets or clears an Area. Clearing a Device's Area leaves it without
// one; clearing an Entity's Area makes it inherit its Device's Area.
type AreaEdit struct{ AreaID *LabelID }

// TagEdit replaces the whole set of tags assigned directly to a Device or
// Entity. An empty set clears them.
type TagEdit struct{ TagIDs []LabelID }

type DevicePatch struct {
	NameEdit *NameEdit
	AreaEdit *AreaEdit
	TagEdit  *TagEdit
}

type EntityPatch struct {
	Enabled  *bool
	NameEdit *NameEdit
	AreaEdit *AreaEdit
	TagEdit  *TagEdit
}

func NormalizeNameOverride(value string) (string, error) {
	normalized, err := normalizeDisplayName(value)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidMetadataPatch, err)
	}
	return normalized, nil
}

// normalizeDisplayName holds the rules shared by name overrides and Label
// names. Callers attach their own error class.
func normalizeDisplayName(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New("name must be valid UTF-8")
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return "", errors.New("name must not contain control characters")
		}
	}
	value = strings.TrimSpace(value)
	if length := utf8.RuneCountInString(value); length < 1 || length > 128 {
		return "", errors.New("name must contain 1–128 code points")
	}
	return value, nil
}

// PrepareEntityPatch validates freely constructed Entity edits and returns
// owned, normalized values. Service and repository are independent input
// boundaries. Whether an assigned Label exists and has the right kind is a
// changing fact, checked in the write transaction.
func PrepareEntityPatch(patch EntityPatch) (EntityPatch, error) {
	if patch.Enabled == nil && patch.NameEdit == nil && patch.AreaEdit == nil && patch.TagEdit == nil {
		return EntityPatch{}, ErrInvalidMetadataPatch
	}
	prepared := EntityPatch{}
	if patch.Enabled != nil {
		value := *patch.Enabled
		prepared.Enabled = &value
	}
	var err error
	prepared.NameEdit, prepared.AreaEdit, prepared.TagEdit, err = prepareSharedEdits(
		patch.NameEdit, patch.AreaEdit, patch.TagEdit,
	)
	if err != nil {
		return EntityPatch{}, err
	}
	return prepared, nil
}

// PrepareDevicePatch validates freely constructed Device edits. See
// PrepareEntityPatch.
func PrepareDevicePatch(patch DevicePatch) (DevicePatch, error) {
	if patch.NameEdit == nil && patch.AreaEdit == nil && patch.TagEdit == nil {
		return DevicePatch{}, ErrInvalidMetadataPatch
	}
	name, area, tags, err := prepareSharedEdits(patch.NameEdit, patch.AreaEdit, patch.TagEdit)
	if err != nil {
		return DevicePatch{}, err
	}
	return DevicePatch{NameEdit: name, AreaEdit: area, TagEdit: tags}, nil
}

// prepareSharedEdits prepares the edits Devices and Entities share. An absent
// edit stays absent.
func prepareSharedEdits(name *NameEdit, area *AreaEdit, tags *TagEdit) (*NameEdit, *AreaEdit, *TagEdit, error) {
	var preparedName *NameEdit
	var preparedArea *AreaEdit
	var preparedTags *TagEdit
	var err error
	if name != nil {
		if preparedName, err = prepareNameEdit(*name); err != nil {
			return nil, nil, nil, err
		}
	}
	if area != nil {
		if preparedArea, err = prepareAreaEdit(*area); err != nil {
			return nil, nil, nil, err
		}
	}
	if tags != nil {
		if preparedTags, err = prepareTagEdit(*tags); err != nil {
			return nil, nil, nil, err
		}
	}
	return preparedName, preparedArea, preparedTags, nil
}

func prepareNameEdit(edit NameEdit) (*NameEdit, error) {
	prepared := &NameEdit{}
	if edit.Override != nil {
		value, err := NormalizeNameOverride(*edit.Override)
		if err != nil {
			return nil, err
		}
		prepared.Override = &value
	}
	return prepared, nil
}

func prepareAreaEdit(edit AreaEdit) (*AreaEdit, error) {
	prepared := &AreaEdit{}
	if edit.AreaID != nil {
		id, err := ParseLabelID(string(*edit.AreaID))
		if err != nil {
			return nil, fmt.Errorf("%w: area: %w", ErrInvalidMetadataPatch, err)
		}
		prepared.AreaID = &id
	}
	return prepared, nil
}

// prepareTagEdit parses, deduplicates, and sorts tag IDs so equal sets compare
// equal and persistence writes them in one order.
func prepareTagEdit(edit TagEdit) (*TagEdit, error) {
	ids := make([]LabelID, 0, len(edit.TagIDs))
	for _, raw := range edit.TagIDs {
		id, err := ParseLabelID(string(raw))
		if err != nil {
			return nil, fmt.Errorf("%w: tag: %w", ErrInvalidMetadataPatch, err)
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) > MaxTagsPerTarget {
		return nil, fmt.Errorf("%w: at most %d tags", ErrInvalidMetadataPatch, MaxTagsPerTarget)
	}
	return &TagEdit{TagIDs: ids}, nil
}

func (service *Service) PatchDevice(ctx context.Context, id DeviceID, patch DevicePatch) (Device, error) {
	if _, err := ParseDeviceID(string(id)); err != nil {
		return Device{}, fmt.Errorf("%w: %w", ErrInvalidMetadataPatch, err)
	}
	prepared, err := PrepareDevicePatch(patch)
	if err != nil {
		return Device{}, err
	}
	now := service.dependencies.Now().UTC()
	if now.IsZero() {
		return Device{}, fmt.Errorf("metadata clock returned zero time")
	}
	device, err := service.stores.Metadata.PatchDevice(
		ctx,
		PatchDeviceParams{DeviceID: id, Patch: prepared, UpdatedAt: now},
	)
	return CopyDevice(device), err
}

func (service *Service) PatchEntity(ctx context.Context, id EntityID, patch EntityPatch) (EntityWithState, error) {
	if _, err := ParseEntityID(string(id)); err != nil {
		return EntityWithState{}, fmt.Errorf("%w: %w", ErrInvalidMetadataPatch, err)
	}
	prepared, err := PrepareEntityPatch(patch)
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
	device.Labels = CopyDeviceLabels(device.Labels)
	return device
}
