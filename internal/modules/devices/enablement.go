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
	return service.setEntityEnabled(ctx, entityID, enabled, nil, nil)
}

func (service *Service) SetOwnedEntityEnabled(
	ctx context.Context,
	adapterID string,
	runtimeID RuntimeID,
	entityID EntityID,
	enabled bool,
) (bool, error) {
	if !registrationSlugPattern.MatchString(adapterID) {
		return false, errors.New("adapter ID must be a subject-safe slug")
	}
	if _, err := ParseRuntimeID(string(runtimeID)); err != nil {
		return false, fmt.Errorf("parse runtime ID: %w", err)
	}
	view, err := service.setEntityEnabled(ctx, entityID, enabled, &adapterID, &runtimeID)
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
	requiredRuntime *RuntimeID,
) (EntityWithState, error) {
	if _, err := ParseEntityID(string(entityID)); err != nil {
		return EntityWithState{}, fmt.Errorf("parse entity ID: %w", err)
	}
	updatedAt := service.dependencies.Now().UTC()
	if updatedAt.IsZero() {
		return EntityWithState{}, errors.New("enablement clock returned zero time")
	}
	snapshot := service.healthEvaluationSnapshot()
	view, err := service.repository.SetEntityEnabled(ctx, SetEntityEnabledParams{
		EntityID: entityID, Enabled: enabled, RequiredOwner: requiredOwner,
		RequiredRuntime: requiredRuntime, UpdatedAt: updatedAt,
	})
	if err != nil {
		return EntityWithState{}, err
	}
	return evaluateEntityAvailability(copyEntityWithState(view), snapshot, updatedAt), nil
}
