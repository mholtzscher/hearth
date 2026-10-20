package automations_test

import (
	"context"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type startupCutoffRepository struct {
	automations.Repository

	cutoff time.Time
}

func (repository *startupCutoffRepository) AdmitDeviceFact(
	ctx context.Context,
	fact automations.DeviceFact,
	snapshot devices.EntityStateSnapshot,
	now time.Time,
	startupAt time.Time,
) (automations.AdmissionResult, error) {
	repository.cutoff = startupAt
	return repository.Repository.AdmitDeviceFact(ctx, fact, snapshot, now, startupAt)
}
