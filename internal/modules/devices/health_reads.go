package devices

import (
	"context"
	"errors"
	"fmt"
)

func (service *Service) ListAdapters(
	ctx context.Context,
	params ListAdaptersParams,
) (Page[AdapterInstance], error) {
	if !ValidPageLimit(params.Limit) {
		return Page[AdapterInstance]{}, ErrInvalidPage
	}
	if params.AfterID != nil && !registrationSlugPattern.MatchString(*params.AfterID) {
		return Page[AdapterInstance]{}, fmt.Errorf("%w: Adapter position must be a subject-safe slug", ErrInvalidPage)
	}
	page, err := service.stores.Adapters.ListAdapters(ctx, params)
	if err != nil {
		return Page[AdapterInstance]{}, err
	}
	items := make([]AdapterInstance, len(page.Items))
	for index, instance := range page.Items {
		items[index] = copyAdapterInstance(instance)
	}
	return Page[AdapterInstance]{Items: items, HasMore: page.HasMore}, nil
}

func (service *Service) GetAdapter(ctx context.Context, adapterID string) (AdapterInstance, error) {
	if !registrationSlugPattern.MatchString(adapterID) {
		return AdapterInstance{}, errors.New("adapter ID must be a subject-safe slug")
	}
	instance, err := service.stores.Adapters.GetAdapter(ctx, adapterID)
	if err != nil {
		return AdapterInstance{}, err
	}
	return copyAdapterInstance(instance), nil
}

func (service *Service) ListAdapterHealthHistory(
	ctx context.Context,
	params ListAdapterHealthParams,
) (Page[HealthTransition], error) {
	if !registrationSlugPattern.MatchString(params.AdapterID) || !ValidPageLimit(params.Limit) ||
		(params.BeforeReceiveOrder != nil && *params.BeforeReceiveOrder < 1) {
		return Page[HealthTransition]{}, ErrInvalidPage
	}
	page, err := service.stores.Adapters.ListAdapterHealthHistory(ctx, params)
	if err != nil {
		return Page[HealthTransition]{}, err
	}
	return copyHealthTransitionPage(page), nil
}

func (service *Service) ListEntityAvailabilityHistory(
	ctx context.Context,
	params ListEntityAvailabilityParams,
) (Page[HealthTransition], error) {
	if _, err := ParseEntityID(string(params.EntityID)); err != nil || !ValidPageLimit(params.Limit) ||
		(params.BeforeReceiveOrder != nil && *params.BeforeReceiveOrder < 1) {
		return Page[HealthTransition]{}, ErrInvalidPage
	}
	page, err := service.stores.Adapters.ListEntityAvailabilityHistory(ctx, params)
	if err != nil {
		return Page[HealthTransition]{}, err
	}
	return copyHealthTransitionPage(page), nil
}

func copyHealthTransitionPage(page Page[HealthTransition]) Page[HealthTransition] {
	items := make([]HealthTransition, len(page.Items))
	for index, transition := range page.Items {
		items[index] = copyHealthTransition(transition)
	}
	return Page[HealthTransition]{Items: items, HasMore: page.HasMore}
}

func copyAdapterInstance(instance AdapterInstance) AdapterInstance {
	cloned := instance
	cloned.Health.Reason = CopyHealthReason(instance.Health.Reason)
	if instance.Health.SourceObservedAt != nil {
		sourceObservedAt := *instance.Health.SourceObservedAt
		cloned.Health.SourceObservedAt = &sourceObservedAt
	}
	if instance.Health.Runtime != nil {
		runtime := *instance.Health.Runtime
		if instance.Health.Runtime.LastHeartbeatAt != nil {
			lastHeartbeatAt := *instance.Health.Runtime.LastHeartbeatAt
			runtime.LastHeartbeatAt = &lastHeartbeatAt
		}
		cloned.Health.Runtime = &runtime
	}
	return cloned
}

func copyHealthTransition(transition HealthTransition) HealthTransition {
	cloned := transition
	cloned.Reason = CopyHealthReason(transition.Reason)
	if transition.SourceObservedAt != nil {
		sourceObservedAt := *transition.SourceObservedAt
		cloned.SourceObservedAt = &sourceObservedAt
	}
	return cloned
}
