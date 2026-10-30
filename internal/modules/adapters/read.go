package adapters

import (
	"context"
	"fmt"
)

func (service *Service) ListAdapters(
	ctx context.Context,
	params ListAdaptersParams,
) (Page[AdapterInstance], error) {
	if !ValidPageLimit(params.Limit) {
		return Page[AdapterInstance]{}, ErrInvalidPage
	}
	if params.AfterID != nil && ValidateAdapterID(*params.AfterID) != nil {
		return Page[AdapterInstance]{}, fmt.Errorf("%w: Adapter position must be a subject-safe slug", ErrInvalidPage)
	}
	page, err := service.stores.Reads.ListAdapters(ctx, params)
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
	if err := ValidateAdapterID(adapterID); err != nil {
		return AdapterInstance{}, err
	}
	instance, err := service.stores.Reads.GetAdapter(ctx, adapterID)
	if err != nil {
		return AdapterInstance{}, err
	}
	return copyAdapterInstance(instance), nil
}

func (service *Service) ListAdapterHealthHistory(
	ctx context.Context,
	params ListAdapterHealthParams,
) (Page[HealthTransition], error) {
	if ValidateAdapterID(params.AdapterID) != nil || !ValidPageLimit(params.Limit) ||
		(params.BeforeReceiveOrder != nil && *params.BeforeReceiveOrder < 1) {
		return Page[HealthTransition]{}, ErrInvalidPage
	}
	page, err := service.stores.Reads.ListAdapterHealthHistory(ctx, params)
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
