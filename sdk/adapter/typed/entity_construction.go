package typed

import (
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/entitytypes"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// EntityObservationInput carries the typed Entity observation data shared by
// every generated SDK facade.
type EntityObservationInput[State, Support any] struct {
	EntityID          string
	Support           Support
	State             State
	AdapterReceivedAt time.Time
	SourceUpdatedAt   *time.Time
}

// NewTypedEntityDescriptor validates support against its schema and builds the
// Entity descriptor with normalized support.
func NewTypedEntityDescriptor[Support any](
	metadata adapter.EntityMetadata,
	typeID string,
	support Support,
	supportCodec *entitytypes.JSONCodec[Support],
) (adapter.EntityDescriptor, error) {
	normalized, err := supportCodec.Encode(support)
	if err != nil {
		return adapter.EntityDescriptor{}, &adapter.ValidationError{
			Err: fmt.Errorf("invalid Entity support: %w", err),
		}
	}
	return adapter.EntityDescriptor{
		Key:        metadata.Key,
		ExternalID: metadata.ExternalID,
		Name:       metadata.Name,
		Type:       typeID,
		Support:    normalized,
	}, nil
}

// NewTypedEntityObservation validates the Entity identity, timestamps, and
// support before checking State, then returns the observation with normalized
// value and UTC formatted times.
func NewTypedEntityObservation[State, Support any](
	input EntityObservationInput[State, Support],
	stateCodec *entitytypes.JSONCodec[State],
	supportCodec *entitytypes.JSONCodec[Support],
	validateState func(Support, State) error,
) (adapter.Observation, error) {
	if input.EntityID == "" {
		return adapter.Observation{}, observationError("Observation entity ID is required")
	}
	if input.AdapterReceivedAt.IsZero() {
		return adapter.Observation{}, observationError("Observation adapter received time is required")
	}
	if input.SourceUpdatedAt != nil && input.SourceUpdatedAt.IsZero() {
		return adapter.Observation{}, observationError("Observation source updated time must be non-zero")
	}
	if _, err := supportCodec.Encode(input.Support); err != nil {
		return adapter.Observation{}, &adapter.ValidationError{
			Err: fmt.Errorf("invalid Entity support: %w", err),
		}
	}
	if err := validateState(input.Support, input.State); err != nil {
		return adapter.Observation{}, &adapter.ValidationError{
			Err: fmt.Errorf("unsupported State: %w", err),
		}
	}
	value, err := stateCodec.Encode(input.State)
	if err != nil {
		return adapter.Observation{}, &adapter.ValidationError{
			Err: fmt.Errorf("invalid State: %w", err),
		}
	}
	observation := adapter.Observation{
		EntityID:          input.EntityID,
		Value:             value,
		AdapterReceivedAt: input.AdapterReceivedAt.UTC().Format(time.RFC3339Nano),
	}
	if input.SourceUpdatedAt != nil {
		formatted := input.SourceUpdatedAt.UTC().Format(time.RFC3339Nano)
		observation.SourceUpdatedAt = &formatted
	}
	return observation, nil
}

// observationError wraps a facade validation failure for typed Entity construction.
func observationError(message string) error {
	return &adapter.ValidationError{Err: errors.New(message)}
}
