package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

func (service *Service) ReceiveObservation(
	ctx context.Context,
	received ReceivedObservation,
) (ObservationReceipt, error) {
	done, err := service.beginWork(ctx, true)
	if err != nil {
		return ObservationReceipt{}, err
	}
	defer done()
	requestContext, cancelRequest := service.requestContext(ctx)
	defer cancelRequest()

	if !registrationSlugPattern.MatchString(received.AdapterID) {
		return ObservationReceipt{}, errors.New("adapter ID must be a subject-safe slug")
	}
	observation := received.Observation
	if _, err := ParseObservationID(string(observation.ID)); err != nil {
		return ObservationReceipt{}, fmt.Errorf("parse observation ID: %w", err)
	}
	if _, err := ParseEntityID(string(observation.EntityID)); err != nil {
		return ObservationReceipt{}, fmt.Errorf("parse observation entity ID: %w", err)
	}
	if !json.Valid(observation.Value) {
		return ObservationReceipt{}, errors.New("observation value must contain one valid JSON value")
	}
	if observation.AdapterReceivedAt.IsZero() {
		return ObservationReceipt{}, errors.New("observation adapter_received_at is required")
	}
	if received.ObservedAt.IsZero() {
		return ObservationReceipt{}, errors.New("observation observed_at is required")
	}
	if observation.RefreshForCommand != nil {
		if _, err := ParseCommandID(string(*observation.RefreshForCommand)); err != nil {
			return ObservationReceipt{}, fmt.Errorf("parse refresh command ID: %w", err)
		}
	}

	received = copyReceivedObservation(received)
	received.ObservedAt = received.ObservedAt.UTC()
	projection, err := service.projectObservation(
		requestContext,
		received,
		received.ObservedAt.Add(service.controls.receiptRetention),
	)
	if err != nil {
		return ObservationReceipt{}, operationError(requestContext, err)
	}
	if projection.satisfiedCommand != nil {
		service.notifyCommand(*projection.satisfiedCommand)
	}
	return copyObservationReceipt(projection.receipt), nil
}

func (service *Service) GetEntity(ctx context.Context, id EntityID) (EntityView, error) {
	done, err := service.beginWork(ctx, false)
	if err != nil {
		return EntityView{}, err
	}
	defer done()
	requestContext, cancelRequest := service.requestContext(ctx)
	defer cancelRequest()

	if _, err := ParseEntityID(string(id)); err != nil {
		return EntityView{}, fmt.Errorf("parse entity ID: %w", err)
	}
	view, err := getEntityView(requestContext, service.database, id)
	if err != nil {
		return EntityView{}, operationError(requestContext, err)
	}
	return copyEntityView(view), nil
}

func copyReceivedObservation(received ReceivedObservation) ReceivedObservation {
	copy := received
	copy.Observation = copyObservation(received.Observation)
	return copy
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

func copyObservationReceipt(receipt ObservationReceipt) ObservationReceipt {
	copy := receipt
	if receipt.Rejection != nil {
		rejection := *receipt.Rejection
		copy.Rejection = &rejection
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
