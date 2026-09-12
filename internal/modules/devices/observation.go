package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

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
	if _, err := ParseCorrelationID(string(observation.CorrelationID)); err != nil {
		return ProjectionResult{}, fmt.Errorf("parse observation correlation ID: %w", err)
	}

	observedAt = observedAt.UTC()
	params := ProjectObservationParams{
		AdapterID:   adapterID,
		RuntimeID:   runtimeID,
		Observation: copyObservation(observation),
		ObservedAt:  observedAt,
		Now:         service.dependencies.Now,
	}
	// An Observation that refreshes a Command competes with that Command's own
	// lifecycle for the same durable row, so it takes the Command's transition
	// stripe before opening the transaction and holds it through waiter
	// notification and fact enqueue. Unrelated Commands hash to their own
	// stripes and are unaffected.
	release := func() {}
	if observation.RefreshForCommand != nil {
		release = service.commandTransitions.lock(*observation.RefreshForCommand)
	}
	defer release()
	result, err := service.stores.Observations.ProjectObservation(ctx, params)
	if err != nil {
		return ProjectionResult{}, err
	}
	// The waiter is notified before any transport work so fact enqueue latency
	// can never delay authoritative Command completion.
	if result.SatisfiedCommand != nil {
		service.notifyCommand(*result.SatisfiedCommand)
	}
	// Order is part of the contract: the Observation evidence for the
	// committing transaction is enqueued before the Command transition that
	// the same transaction satisfied.
	service.emitObservationFact(ctx, params.Observation, result, observedAt)
	if result.SatisfiedCommandRecord != nil {
		service.emitCommandTransition(ctx, *result.SatisfiedCommandRecord)
	}
	return copyProjectionResult(result), nil
}

// DeleteExpiredObservations deletes non-current observations older than the
// retention window. The cutoff derives from the supplied Core now minus the
// retention window and is compared against core-owned observed_at on each
// prune, so retention policy changes apply to already persisted rows.
// Adapter and source timestamps never affect eligibility.
func (service *Service) DeleteExpiredObservations(
	ctx context.Context,
	now time.Time,
	retention time.Duration,
) error {
	if now.IsZero() {
		return errors.New("observation prune time is required")
	}
	if retention <= 0 {
		return errors.New("observation retention must be positive")
	}
	return service.stores.Observations.DeleteExpiredObservations(
		ctx,
		now.UTC().Add(-retention),
	)
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
		if result.SatisfiedCommand.ObservationID != nil {
			observationID := *result.SatisfiedCommand.ObservationID
			command.ObservationID = &observationID
		}
		if result.SatisfiedCommand.Value != nil {
			value := append(Value(nil), *result.SatisfiedCommand.Value...)
			command.Value = &value
		}
		cloned.SatisfiedCommand = &command
	}
	if result.SatisfiedCommandRecord != nil {
		record := copyCommandRecord(*result.SatisfiedCommandRecord)
		cloned.SatisfiedCommandRecord = &record
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
