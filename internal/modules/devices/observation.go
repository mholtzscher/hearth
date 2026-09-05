package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const ObservationRetention = 192 * time.Hour

func (service *Service) ProjectObservation(
	ctx context.Context,
	adapterID string,
	runtimeID RuntimeID,
	observation Observation,
	observedAt time.Time,
) (ProjectionResult, error) {
	if !registrationSlugPattern.MatchString(adapterID) {
		return ProjectionResult{}, errors.New("adapter ID must be a subject-safe slug")
	}
	if _, err := ParseRuntimeID(string(runtimeID)); err != nil {
		return ProjectionResult{}, fmt.Errorf("parse runtime ID: %w", err)
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
		AdapterID:   adapterID,
		RuntimeID:   runtimeID,
		Observation: copyObservation(observation),
		ObservedAt:  observedAt,
		Now:         service.dependencies.Now,
		ExpiresAt:   observedAt.Add(ObservationRetention),
	}
	result, err := service.stores.Observations.ProjectObservation(ctx, params)
	if err != nil {
		return ProjectionResult{}, err
	}
	if result.SatisfiedCommand != nil {
		service.notifyCommand(*result.SatisfiedCommand)
	}
	return copyProjectionResult(result), nil
}

func (service *Service) DeleteExpiredObservations(ctx context.Context, before time.Time) error {
	if before.IsZero() {
		return errors.New("observation expiry cutoff is required")
	}
	return service.stores.Observations.DeleteExpiredObservations(ctx, before.UTC())
}

func copyObservation(observation Observation) Observation {
	cloned := observation
	cloned.Value = append(Value(nil), observation.Value...)
	if observation.SourceUpdatedAt != nil {
		sourceUpdatedAt := *observation.SourceUpdatedAt
		cloned.SourceUpdatedAt = &sourceUpdatedAt
	}
	if observation.RefreshForCommand != nil {
		commandID := *observation.RefreshForCommand
		cloned.RefreshForCommand = &commandID
	}
	return cloned
}

func copyProjectionResult(result ProjectionResult) ProjectionResult {
	cloned := result
	if result.State != nil {
		state := copyState(*result.State)
		cloned.State = &state
	}
	if result.Rejection != nil {
		rejection := *result.Rejection
		cloned.Rejection = &rejection
	}
	if result.SatisfiedCommand != nil {
		command := *result.SatisfiedCommand
		command.Value = append(Value(nil), result.SatisfiedCommand.Value...)
		cloned.SatisfiedCommand = &command
	}
	return cloned
}

func copyEntityWithState(view EntityWithState) EntityWithState {
	cloned := view
	cloned.Entity.Support = append(EntitySupport(nil), view.Entity.Support...)
	if view.State != nil {
		state := copyState(*view.State)
		cloned.State = &state
	}
	if view.Availability.SourceObservedAt != nil {
		sourceObservedAt := *view.Availability.SourceObservedAt
		cloned.Availability.SourceObservedAt = &sourceObservedAt
	}
	cloned.Availability.Reason = copyHealthReason(view.Availability.Reason)
	return cloned
}

func copyState(state State) State {
	cloned := state
	cloned.Value = append(Value(nil), state.Value...)
	if state.SourceUpdatedAt != nil {
		sourceUpdatedAt := *state.SourceUpdatedAt
		cloned.SourceUpdatedAt = &sourceUpdatedAt
	}
	return cloned
}
