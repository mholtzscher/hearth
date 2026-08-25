package nats

import (
	"context"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

type ObservationReceiver interface {
	ReceiveObservation(context.Context, devices.ReceivedObservation) (devices.ObservationReceipt, error)
}

func ObservationHandler(receiver ObservationReceiver) platformnats.ObservationHandler {
	return func(ctx context.Context, delivery platformnats.ObservationDelivery) error {
		received, err := receivedObservation(delivery)
		if err != nil {
			return err
		}
		_, err = receiver.ReceiveObservation(ctx, received)
		return err
	}
}

func receivedObservation(delivery platformnats.ObservationDelivery) (devices.ReceivedObservation, error) {
	envelope := delivery.Envelope
	observationID, err := devices.ParseObservationID(envelope.ID)
	if err != nil {
		return devices.ReceivedObservation{}, err
	}
	entityID, err := devices.ParseEntityID(envelope.Data.EntityID)
	if err != nil {
		return devices.ReceivedObservation{}, err
	}
	adapterReceivedAt, err := time.Parse(time.RFC3339Nano, envelope.Data.AdapterReceivedAt)
	if err != nil {
		return devices.ReceivedObservation{}, err
	}
	observation := devices.Observation{
		ID: observationID, EntityID: entityID,
		Value:             devices.Value(append([]byte(nil), envelope.Data.Value...)),
		AdapterReceivedAt: adapterReceivedAt,
	}
	if envelope.Data.SourceUpdatedAt != nil {
		sourceUpdatedAt, err := time.Parse(time.RFC3339Nano, *envelope.Data.SourceUpdatedAt)
		if err != nil {
			return devices.ReceivedObservation{}, err
		}
		observation.SourceUpdatedAt = &sourceUpdatedAt
	}
	if envelope.Data.RefreshForCommand != nil {
		commandID, err := devices.ParseCommandID(*envelope.Data.RefreshForCommand)
		if err != nil {
			return devices.ReceivedObservation{}, err
		}
		observation.RefreshForCommand = &commandID
	}
	return devices.ReceivedObservation{
		AdapterID: delivery.Route.AdapterID, Observation: observation, ObservedAt: delivery.ObservedAt.UTC(),
	}, nil
}
