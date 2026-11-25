package devices

import (
	"context"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

// SetOwnedEntityEnabled applies an Adapter-initiated enablement change. The
// repository fences it to the owning Adapter's active runtime. Household
// enablement changes use PatchEntity.
func (service *Service) SetOwnedEntityEnabled(
	ctx context.Context,
	adapterID string,
	runtimeID adapters.RuntimeID,
	entityID EntityID,
	enabled bool,
) (bool, error) {
	if err := adapters.ValidateAdapterID(adapterID); err != nil {
		return false, err
	}
	if _, err := adapters.ParseRuntimeID(string(runtimeID)); err != nil {
		return false, fmt.Errorf("parse runtime ID: %w", err)
	}
	if _, err := ParseEntityID(string(entityID)); err != nil {
		return false, fmt.Errorf("parse entity ID: %w", err)
	}
	updatedAt := service.dependencies.Now().UTC()
	if updatedAt.IsZero() {
		return false, errors.New("enablement clock returned zero time")
	}
	view, err := service.stores.Enablement.SetEntityEnabled(ctx, SetEntityEnabledParams{
		EntityID: entityID, Enabled: enabled, RequiredOwner: &adapterID,
		RequiredRuntime: &runtimeID, UpdatedAt: updatedAt,
	})
	if err != nil {
		return false, err
	}
	return view.Entity.Enabled, nil
}
