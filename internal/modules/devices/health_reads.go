package devices

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (service *Service) ListAdapters(
	ctx context.Context,
	params ListAdaptersParams,
) (Page[AdapterInstance], error) {
	if !validPageLimit(params.Limit) {
		return Page[AdapterInstance]{}, ErrInvalidPage
	}
	if params.AfterID != nil && !registrationSlugPattern.MatchString(*params.AfterID) {
		return Page[AdapterInstance]{}, fmt.Errorf("%w: Adapter position must be a subject-safe slug", ErrInvalidPage)
	}
	snapshot := service.healthEvaluationSnapshot()
	now := service.dependencies.Now().UTC()
	page, err := service.repository.ListAdapters(ctx, params)
	if err != nil {
		return Page[AdapterInstance]{}, err
	}
	items := make([]AdapterInstance, len(page.Items))
	for index, instance := range page.Items {
		items[index] = evaluateAdapterHealth(copyAdapterInstance(instance), snapshot, now)
	}
	return Page[AdapterInstance]{Items: items, HasMore: page.HasMore}, nil
}

func (service *Service) GetAdapter(ctx context.Context, adapterID string) (AdapterInstance, error) {
	if !registrationSlugPattern.MatchString(adapterID) {
		return AdapterInstance{}, errors.New("adapter ID must be a subject-safe slug")
	}
	snapshot := service.healthEvaluationSnapshot()
	now := service.dependencies.Now().UTC()
	instance, err := service.repository.GetAdapter(ctx, adapterID)
	if err != nil {
		return AdapterInstance{}, err
	}
	return evaluateAdapterHealth(copyAdapterInstance(instance), snapshot, now), nil
}

func (service *Service) ArchiveAdapter(ctx context.Context, adapterID string) error {
	if !registrationSlugPattern.MatchString(adapterID) {
		return errors.New("adapter ID must be a subject-safe slug")
	}
	return service.repository.ArchiveAdapter(ctx, ArchiveAdapterParams{
		AdapterID: adapterID, ArchivedAt: service.dependencies.Now().UTC(),
	})
}

func (service *Service) ListAdapterHealthHistory(
	ctx context.Context,
	params ListAdapterHealthParams,
) (Page[HealthTransition], error) {
	if !registrationSlugPattern.MatchString(params.AdapterID) || !validPageLimit(params.Limit) ||
		(params.BeforeReceiveOrder != nil && *params.BeforeReceiveOrder < 1) {
		return Page[HealthTransition]{}, ErrInvalidPage
	}
	page, err := service.repository.ListAdapterHealthHistory(ctx, params)
	if err != nil {
		return Page[HealthTransition]{}, err
	}
	return copyHealthTransitionPage(page), nil
}

func (service *Service) ListEntityAvailabilityHistory(
	ctx context.Context,
	params ListEntityAvailabilityParams,
) (Page[HealthTransition], error) {
	if _, err := ParseEntityID(string(params.EntityID)); err != nil || !validPageLimit(params.Limit) ||
		(params.BeforeReceiveOrder != nil && *params.BeforeReceiveOrder < 1) {
		return Page[HealthTransition]{}, ErrInvalidPage
	}
	page, err := service.repository.ListEntityAvailabilityHistory(ctx, params)
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

func evaluateAdapterHealth(
	instance AdapterInstance,
	snapshot healthEvaluationSnapshot,
	now time.Time,
) AdapterInstance {
	if !snapshot.active || !now.Before(snapshot.recoveryUntil) || instance.Health == nil ||
		instance.Health.Runtime == nil || instance.Health.Runtime.Status != runtimeStatusOnline {
		return instance
	}
	if _, refreshed := snapshot.refreshed[instance.Health.Runtime.ID]; refreshed {
		return instance
	}
	instance.Health.Status = AdapterHealthUnknown
	instance.Health.Since = snapshot.resumedAt
	instance.Health.EvidenceAt = snapshot.resumedAt
	instance.Health.Reason = &HealthReason{Code: "hearth.core_recovering"}
	return instance
}

func copyAdapterInstance(instance AdapterInstance) AdapterInstance {
	cloned := instance
	if instance.ArchivedAt != nil {
		archivedAt := *instance.ArchivedAt
		cloned.ArchivedAt = &archivedAt
	}
	if instance.Health == nil {
		return cloned
	}
	health := *instance.Health
	health.Reason = copyHealthReason(instance.Health.Reason)
	if instance.Health.Runtime != nil {
		runtime := *instance.Health.Runtime
		if instance.Health.Runtime.LastHeartbeatAt != nil {
			lastHeartbeatAt := *instance.Health.Runtime.LastHeartbeatAt
			runtime.LastHeartbeatAt = &lastHeartbeatAt
		}
		health.Runtime = &runtime
	}
	if instance.Health.ExternalSystem != nil {
		externalSystem := *instance.Health.ExternalSystem
		externalSystem.Reason = copyHealthReason(instance.Health.ExternalSystem.Reason)
		health.ExternalSystem = &externalSystem
	}
	cloned.Health = &health
	return cloned
}

func copyHealthTransition(transition HealthTransition) HealthTransition {
	cloned := transition
	cloned.Reason = copyHealthReason(transition.Reason)
	if transition.SourceObservedAt != nil {
		sourceObservedAt := *transition.SourceObservedAt
		cloned.SourceObservedAt = &sourceObservedAt
	}
	return cloned
}
