package sqlite

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func (repository *DeviceRepository) SetEntityEnabled(
	ctx context.Context,
	params devices.SetEntityEnabledParams,
) (devices.EntityWithState, error) {
	patch, err := devices.PrepareMetadataPatch(&params.Enabled, nil)
	if err != nil {
		return devices.EntityWithState{}, err
	}
	return repository.mutateEntity(
		ctx,
		params.EntityID,
		patch,
		params.UpdatedAt,
		params.RequiredOwner,
		params.RequiredRuntime,
	)
}
