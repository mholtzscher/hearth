package devices

import "context"

func (service *Service) ListEntityAvailabilityHistory(
	ctx context.Context,
	params ListEntityAvailabilityParams,
) (Page[HealthTransition], error) {
	if _, err := ParseEntityID(string(params.EntityID)); err != nil || !ValidPageLimit(params.Limit) ||
		(params.BeforeReceiveOrder != nil && *params.BeforeReceiveOrder < 1) {
		return Page[HealthTransition]{}, ErrInvalidPage
	}
	page, err := service.stores.AvailabilityHistory.ListEntityAvailabilityHistory(ctx, params)
	if err != nil {
		return Page[HealthTransition]{}, err
	}
	return copyHealthTransitionPage(page), nil
}

func copyHealthTransitionPage(page Page[HealthTransition]) Page[HealthTransition] {
	items := make([]HealthTransition, len(page.Items))
	for index, transition := range page.Items {
		items[index] = transition
		items[index].Reason = CopyHealthReason(transition.Reason)
		if transition.SourceObservedAt != nil {
			at := *transition.SourceObservedAt
			items[index].SourceObservedAt = &at
		}
	}
	return Page[HealthTransition]{Items: items, HasMore: page.HasMore}
}
