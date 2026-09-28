package automations

import (
	"context"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// emptyEntityStateSnapshot represents an admission that has no configured Conditions to evaluate.
func emptyEntityStateSnapshot() devices.EntityStateSnapshot {
	return devices.EntityStateSnapshot{Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{}}
}

// readConditionStateSnapshot reads one complete snapshot through the devices
// seam, preserving [devices.ErrEntityStateSnapshotCorrupt].
func (service *Service) readConditionStateSnapshot(
	ctx context.Context,
	ids []devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	if service.devices == nil {
		return devices.EntityStateSnapshot{}, errors.New("automation condition state source is unavailable")
	}
	return service.devices.GetEntityStateSnapshot(ctx, ids)
}
