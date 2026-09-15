package devices

import (
	"context"
	"errors"
	"fmt"
)

type OwnedMapping struct {
	BindingKey string
	DeviceID   DeviceID
	EntityKey  string
	EntityID   EntityID
}

type OwnedMappingPosition struct {
	BindingKey string
	EntityKey  string
}

type OwnedMappingPageParams struct {
	After *OwnedMappingPosition
	Limit int
}

type ListOwnedMappingsParams struct {
	AdapterID string
	After     *OwnedMappingPosition
	Limit     int
}

func (service *Service) ListOwnedMappings(
	ctx context.Context,
	adapterID string,
	runtimeID RuntimeID,
	page OwnedMappingPageParams,
) (Page[OwnedMapping], error) {
	if !registrationSlugPattern.MatchString(adapterID) || !ValidPageLimit(page.Limit) {
		return Page[OwnedMapping]{}, ErrInvalidPage
	}
	if _, err := ParseRuntimeID(string(runtimeID)); err != nil {
		return Page[OwnedMapping]{}, fmt.Errorf("%w: parse runtime ID: %w", ErrInvalidPage, err)
	}
	if page.After != nil && (!registrationSlugPattern.MatchString(page.After.BindingKey) ||
		!registrationSlugPattern.MatchString(page.After.EntityKey)) {
		return Page[OwnedMapping]{}, ErrInvalidPage
	}
	instance, err := service.stores.OwnedMappings.GetAdapter(ctx, adapterID)
	if errors.Is(err, ErrAdapterNotFound) {
		return Page[OwnedMapping]{}, ErrRuntimeFenced
	}
	if err != nil {
		return Page[OwnedMapping]{}, err
	}
	if instance.Health.Runtime == nil || instance.Health.Runtime.Status != RuntimeStatusOnline ||
		instance.Health.Runtime.ID != runtimeID {
		return Page[OwnedMapping]{}, ErrRuntimeFenced
	}
	result, err := service.stores.OwnedMappings.ListOwnedMappings(ctx, ListOwnedMappingsParams{
		AdapterID: adapterID,
		After:     page.After,
		Limit:     page.Limit,
	})
	if err != nil {
		return Page[OwnedMapping]{}, err
	}
	items := make([]OwnedMapping, len(result.Items))
	copy(items, result.Items)
	return Page[OwnedMapping]{Items: items, HasMore: result.HasMore}, nil
}
