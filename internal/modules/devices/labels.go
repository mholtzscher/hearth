package devices

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// LabelID is the canonical identity of a household Label: "lbl_<uuidv7>".
type LabelID string

// LabelKind is fixed when a Label is created.
type LabelKind string

const (
	// LabelKindArea is single-valued: a Device or Entity has at most one Area,
	// and an Entity's Area overrides its Device's.
	LabelKindArea LabelKind = "area"
	// LabelKindTag is multi-valued: an Entity's effective tags are its own
	// plus its Device's.
	LabelKindTag LabelKind = "tag"
)

const (
	// MaxLabels bounds the catalog so one unpaged read lists every Label.
	MaxLabels = 200
	// MaxTagsPerTarget bounds the tags assigned directly to one Device or Entity.
	MaxTagsPerTarget = 32
	// MaxSelectorLabels bounds the Labels in one EntitySelector.
	MaxSelectorLabels = 8
	// MaxSelectorTypes bounds the Entity types in one EntitySelector.
	MaxSelectorTypes = 16
)

var (
	ErrInvalidLabel          = errors.New("invalid label")
	ErrLabelNotFound         = errors.New("label not found")
	ErrLabelNameConflict     = errors.New("label name conflict")
	ErrLabelLimitReached     = errors.New("label limit reached")
	ErrInvalidEntitySelector = errors.New("invalid entity selector")
)

// Label is a household grouping fact. Its name is display only; the name's
// normalized key is unique across both kinds, so one name means one Label.
type Label struct {
	ID        LabelID
	Name      string
	Kind      LabelKind
	CreatedAt time.Time
	UpdatedAt time.Time
}

// LabelRef names an assigned Label.
type LabelRef struct {
	ID   LabelID
	Name string
}

// AreaSource reports whether an Entity's effective Area is its own or its
// Device's.
type AreaSource string

const (
	AreaSourceDevice AreaSource = "device"
	AreaSourceEntity AreaSource = "entity"
)

// EffectiveArea is an Entity's Area and where it came from.
type EffectiveArea struct {
	Label  LabelRef
	Source AreaSource
}

// EntityLabels are an Entity's effective Labels. Persistence derives them from
// one definition of the override and union rule.
type EntityLabels struct {
	Area *EffectiveArea
	// Tags are deduplicated across sources and ordered by name, then ID.
	Tags []LabelRef
}

// DeviceLabels are the Labels assigned directly to a Device.
type DeviceLabels struct {
	Area *LabelRef
	// Tags are ordered by name, then ID.
	Tags []LabelRef
}

// CreateLabelParams is a freely constructed Label creation request.
type CreateLabelParams struct {
	Name string
	Kind LabelKind
}

// CreateLabelRecord is a prepared Label ready to persist. Persistence derives
// the name's uniqueness key with NormalizeLabelName.
type CreateLabelRecord struct {
	Label Label
}

// RenameLabelRecord is a prepared rename ready to persist.
type RenameLabelRecord struct {
	ID        LabelID
	Name      string
	UpdatedAt time.Time
}

// ParseLabelKind accepts the closed set of Label kinds.
func ParseLabelKind(value string) (LabelKind, error) {
	switch kind := LabelKind(value); kind {
	case LabelKindArea, LabelKindTag:
		return kind, nil
	default:
		return "", fmt.Errorf("%w: kind must be area or tag", ErrInvalidLabel)
	}
}

// NormalizeLabelName applies the display-name rules shared with name
// overrides and returns the name plus its uniqueness key. The key is the
// lower-cased name; it does not apply full Unicode case folding.
func NormalizeLabelName(value string) (string, string, error) {
	name, err := normalizeDisplayName(value)
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrInvalidLabel, err)
	}
	return name, strings.ToLower(name), nil
}

// PrepareCreateLabel validates a freely constructed creation request.
func PrepareCreateLabel(params CreateLabelParams, id LabelID, now time.Time) (CreateLabelRecord, error) {
	if _, err := ParseLabelID(string(id)); err != nil {
		return CreateLabelRecord{}, fmt.Errorf("%w: %w", ErrInvalidLabel, err)
	}
	kind, err := ParseLabelKind(string(params.Kind))
	if err != nil {
		return CreateLabelRecord{}, err
	}
	name, _, err := NormalizeLabelName(params.Name)
	if err != nil {
		return CreateLabelRecord{}, err
	}
	if now.IsZero() {
		return CreateLabelRecord{}, fmt.Errorf("%w: creation time is required", ErrInvalidLabel)
	}
	now = now.UTC()
	return CreateLabelRecord{
		Label: Label{ID: id, Name: name, Kind: kind, CreatedAt: now, UpdatedAt: now},
	}, nil
}

// PrepareRenameLabel validates a freely constructed rename.
func PrepareRenameLabel(id LabelID, name string, now time.Time) (RenameLabelRecord, error) {
	if _, err := ParseLabelID(string(id)); err != nil {
		return RenameLabelRecord{}, fmt.Errorf("%w: %w", ErrInvalidLabel, err)
	}
	normalized, _, err := NormalizeLabelName(name)
	if err != nil {
		return RenameLabelRecord{}, err
	}
	if now.IsZero() {
		return RenameLabelRecord{}, fmt.Errorf("%w: update time is required", ErrInvalidLabel)
	}
	return RenameLabelRecord{ID: id, Name: normalized, UpdatedAt: now.UTC()}, nil
}

// CreateLabel adds a Label to the household catalog. It returns
// ErrInvalidLabel, ErrLabelNameConflict, or ErrLabelLimitReached.
func (service *Service) CreateLabel(ctx context.Context, params CreateLabelParams) (Label, error) {
	id, err := service.dependencies.NewLabelID()
	if err != nil {
		return Label{}, fmt.Errorf("generate label ID: %w", err)
	}
	now := service.dependencies.Now()
	if now.IsZero() {
		return Label{}, errors.New("label clock returned zero time")
	}
	record, err := PrepareCreateLabel(params, id, now)
	if err != nil {
		return Label{}, err
	}
	return service.stores.Labels.CreateLabel(ctx, record)
}

// RenameLabel changes a Label's display name. Kind never changes. Renaming to
// the same name preserves the update time. It returns ErrInvalidLabel,
// ErrLabelNotFound, or ErrLabelNameConflict.
func (service *Service) RenameLabel(ctx context.Context, id LabelID, name string) (Label, error) {
	now := service.dependencies.Now()
	if now.IsZero() {
		return Label{}, errors.New("label clock returned zero time")
	}
	record, err := PrepareRenameLabel(id, name, now)
	if err != nil {
		return Label{}, err
	}
	return service.stores.Labels.RenameLabel(ctx, record)
}

// DeleteLabel removes a Label, clearing Areas that name it and removing its tag
// assignments in the same transaction. It returns ErrLabelNotFound.
func (service *Service) DeleteLabel(ctx context.Context, id LabelID) error {
	if _, err := ParseLabelID(string(id)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidLabel, err)
	}
	return service.stores.Labels.DeleteLabel(ctx, id)
}

// ListLabels returns the bounded catalog ordered by kind, then name, then ID.
func (service *Service) ListLabels(ctx context.Context) ([]Label, error) {
	labels, err := service.stores.Labels.ListLabels(ctx)
	if err != nil {
		return nil, err
	}
	return slices.Clone(labels), nil
}

// CopyEntityLabels returns labels that share no mutable memory with the input.
func CopyEntityLabels(labels EntityLabels) EntityLabels {
	cloned := EntityLabels{Tags: copyLabelRefs(labels.Tags)}
	if labels.Area != nil {
		area := *labels.Area
		cloned.Area = &area
	}
	return cloned
}

// CopyDeviceLabels returns labels that share no mutable memory with the input.
func CopyDeviceLabels(labels DeviceLabels) DeviceLabels {
	cloned := DeviceLabels{Tags: copyLabelRefs(labels.Tags)}
	if labels.Area != nil {
		area := *labels.Area
		cloned.Area = &area
	}
	return cloned
}

func copyLabelRefs(refs []LabelRef) []LabelRef {
	if refs == nil {
		return []LabelRef{}
	}
	return slices.Clone(refs)
}
