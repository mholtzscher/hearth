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
	requestContext, _, done, err := service.beginWork(ctx)
	if err != nil {
		return ObservationReceipt{}, err
	}
	defer done()

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

	received = ReceivedObservation{
		AdapterID:   received.AdapterID,
		Observation: copyObservation(observation),
		ObservedAt:  received.ObservedAt.UTC(),
	}
	projection, err := service.projectObservation(requestContext, received)
	if err != nil {
		return ObservationReceipt{}, normalizeServiceError(requestContext, err)
	}
	if projection.satisfiedCommand != nil {
		service.notifyCommand(*projection.satisfiedCommand)
	}
	return projection.receipt, nil
}

func (service *Service) GetEntity(ctx context.Context, id EntityID) (EntityView, error) {
	requestContext, _, done, err := service.beginWork(ctx)
	if err != nil {
		return EntityView{}, err
	}
	defer done()
	if _, err := ParseEntityID(string(id)); err != nil {
		return EntityView{}, fmt.Errorf("parse entity ID: %w", err)
	}
	view, err := getEntityView(requestContext, service.database, id)
	if err != nil {
		return EntityView{}, normalizeServiceError(requestContext, err)
	}
	return view, nil
}

func copyObservation(observation Observation) Observation {
	copy := observation
	copy.Value = append(Value(nil), observation.Value...)
	copy.SourceUpdatedAt = copyTimePointer(observation.SourceUpdatedAt)
	if observation.RefreshForCommand != nil {
		commandID := *observation.RefreshForCommand
		copy.RefreshForCommand = &commandID
	}
	return copy
}
