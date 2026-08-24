package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const ObservationReceiptRetention = 192 * time.Hour

func (service *Service) ProjectObservation(
	ctx context.Context,
	adapterID string,
	observation Observation,
	observedAt time.Time,
) (ProjectionResult, error) {
	if !registrationSlugPattern.MatchString(adapterID) {
		return ProjectionResult{}, errors.New("adapter ID must be a subject-safe slug")
	}
	if _, err := ParseObservationID(string(observation.ID)); err != nil {
		return ProjectionResult{}, fmt.Errorf("parse observation ID: %w", err)
	}
	if _, err := ParseEntityID(string(observation.EntityID)); err != nil {
		return ProjectionResult{}, fmt.Errorf("parse observation entity ID: %w", err)
	}
	if !json.Valid(observation.Value) {
		return ProjectionResult{}, errors.New("observation value must contain one valid JSON value")
	}
	if observation.AdapterReceivedAt.IsZero() {
		return ProjectionResult{}, errors.New("observation adapter_received_at is required")
	}
	if observedAt.IsZero() {
		return ProjectionResult{}, errors.New("observation observed_at is required")
	}
	if observation.RefreshForCommand != nil {
		if _, err := ParseCommandID(string(*observation.RefreshForCommand)); err != nil {
			return ProjectionResult{}, fmt.Errorf("parse refresh command ID: %w", err)
		}
	}

	observedAt = observedAt.UTC()
	params := ProjectObservationParams{
		AdapterID:        adapterID,
		Observation:      copyObservation(observation),
		ObservedAt:       observedAt,
		Now:              service.dependencies.Now,
		ReceiptExpiresAt: observedAt.Add(ObservationReceiptRetention),
	}
	result, err := service.repository.ProjectObservation(ctx, params)
	if err != nil {
		return ProjectionResult{}, err
	}
	if result.SatisfiedCommand != nil {
		service.notifyCommand(*result.SatisfiedCommand)
	}
	return copyProjectionResult(result), nil
}

func (service *Service) GetEntity(ctx context.Context, id EntityID) (EntityView, error) {
	if _, err := ParseEntityID(string(id)); err != nil {
		return EntityView{}, fmt.Errorf("parse entity ID: %w", err)
	}
	view, err := service.repository.GetEntityView(ctx, id)
	if err != nil {
		return EntityView{}, err
	}
	return copyEntityView(view), nil
}

func (service *Service) DeleteExpiredObservationReceipts(ctx context.Context, before time.Time) error {
	if before.IsZero() {
		return errors.New("receipt expiry cutoff is required")
	}
	return service.repository.DeleteExpiredObservationReceipts(ctx, before.UTC())
}

func copyObservation(observation Observation) Observation {
	copy := observation
	copy.Value = append(Value(nil), observation.Value...)
	if observation.SourceUpdatedAt != nil {
		sourceUpdatedAt := *observation.SourceUpdatedAt
		copy.SourceUpdatedAt = &sourceUpdatedAt
	}
	if observation.RefreshForCommand != nil {
		commandID := *observation.RefreshForCommand
		copy.RefreshForCommand = &commandID
	}
	return copy
}

func copyProjectionResult(result ProjectionResult) ProjectionResult {
	copy := result
	if result.State != nil {
		state := copyState(*result.State)
		copy.State = &state
	}
	if result.Rejection != nil {
		rejection := *result.Rejection
		copy.Rejection = &rejection
	}
	if result.SatisfiedCommand != nil {
		command := *result.SatisfiedCommand
		command.Value = append(Value(nil), result.SatisfiedCommand.Value...)
		copy.SatisfiedCommand = &command
	}
	return copy
}

func copyEntityView(view EntityView) EntityView {
	copy := view
	copy.Entity.Support = append(EntitySupport(nil), view.Entity.Support...)
	if view.State != nil {
		state := copyState(*view.State)
		copy.State = &state
	}
	return copy
}

func copyState(state State) State {
	copy := state
	copy.Value = append(Value(nil), state.Value...)
	if state.SourceUpdatedAt != nil {
		sourceUpdatedAt := *state.SourceUpdatedAt
		copy.SourceUpdatedAt = &sourceUpdatedAt
	}
	return copy
}
