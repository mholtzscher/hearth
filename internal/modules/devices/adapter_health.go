package devices

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

var (
	ErrInvalidAdapterHealthUpdate = errors.New("invalid adapter health update")
	ErrHealthRevisionConflict     = errors.New("health_revision_conflict")
	ErrCorruptHealthProjection    = errors.New("corrupt health projection")
)

type AdapterHealthUpdate struct {
	MessageID string
	Snapshot  adapters.HealthSnapshot
}

type HealthProjectionDisposition string

const (
	HealthProjectionApplied   HealthProjectionDisposition = "applied"
	HealthProjectionDuplicate HealthProjectionDisposition = "duplicate"
	HealthProjectionObsolete  HealthProjectionDisposition = "obsolete"
)

type HealthProjectionResult struct {
	Disposition HealthProjectionDisposition
}

type HealthProjectionRepository interface {
	ApplyAdapterHealth(context.Context, AdapterHealthUpdate, time.Time) (HealthProjectionResult, error)
}

// NormalizeAdapterHealthUpdate prepares owned domain values at independently
// callable service and persistence boundaries. Equality bytes remain repository-owned.
func NormalizeAdapterHealthUpdate(update AdapterHealthUpdate) (AdapterHealthUpdate, error) {
	if _, err := adapters.ParseHealthUpdateID(update.MessageID); err != nil {
		return AdapterHealthUpdate{}, fmt.Errorf("%w: notification identity", ErrInvalidAdapterHealthUpdate)
	}
	snapshot, err := adapters.NormalizeHealthSnapshot(update.Snapshot)
	if err != nil {
		return AdapterHealthUpdate{}, fmt.Errorf("%w: snapshot", ErrInvalidAdapterHealthUpdate)
	}
	return AdapterHealthUpdate{MessageID: update.MessageID, Snapshot: snapshot}, nil
}

func (service *Service) ApplyAdapterHealth(
	ctx context.Context,
	update AdapterHealthUpdate,
) (HealthProjectionResult, error) {
	prepared, err := NormalizeAdapterHealthUpdate(update)
	if err != nil {
		return HealthProjectionResult{}, err
	}
	return service.stores.HealthProjection.ApplyAdapterHealth(ctx, prepared, service.dependencies.Now().UTC())
}
