package devices

import (
	"context"
	"errors"
	"fmt"
)

func (service *Service) SetEntityEnabled(
	ctx context.Context,
	entityID EntityID,
	enabled bool,
) (EntityWithState, error) {
	return service.setEntityEnabled(ctx, entityID, enabled, nil)
}

func (service *Service) SetOwnedEntityEnabled(
	ctx context.Context,
	adapterID string,
	entityID EntityID,
	enabled bool,
) (bool, error) {
	if !registrationSlugPattern.MatchString(adapterID) {
		return false, errors.New("adapter ID must be a subject-safe slug")
	}
	view, err := service.setEntityEnabled(ctx, entityID, enabled, &adapterID)
	if err != nil {
		return false, err
	}
	return view.Entity.Enabled, nil
}

func (service *Service) setEntityEnabled(
	ctx context.Context,
	entityID EntityID,
	enabled bool,
	requiredOwner *string,
) (EntityWithState, error) {
	if _, err := ParseEntityID(string(entityID)); err != nil {
		return EntityWithState{}, fmt.Errorf("parse entity ID: %w", err)
	}
	updatedAt := service.dependencies.Now().UTC()
	if updatedAt.IsZero() {
		return EntityWithState{}, errors.New("enablement clock returned zero time")
	}
	view, err := service.repository.SetEntityEnabled(ctx, SetEntityEnabledParams{
		EntityID: entityID, Enabled: enabled, RequiredOwner: requiredOwner, UpdatedAt: updatedAt,
	})
	if err != nil {
		return EntityWithState{}, err
	}
	return copyEntityWithState(view), nil
}
