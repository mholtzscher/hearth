package nats

import (
	"context"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

type ObservationReceiver interface {
	ReceiveObservation(context.Context, devices.ReceivedObservation) (devices.ObservationReceipt, error)
}

func ObservationHandler(receiver ObservationReceiver) platformnats.ObservationHandler {
	return func(ctx context.Context, delivery platformnats.ObservationDelivery) error {
		observation, err := domainObservation(delivery.Envelope)
		if err != nil {
			return err
		}
		_, err = receiver.ReceiveObservation(ctx, devices.ReceivedObservation{
			AdapterID: delivery.Route.AdapterID, Observation: observation, ObservedAt: delivery.ObservedAt.UTC(),
		})
		return err
	}
}

func domainObservation(envelope platformnats.Envelope[platformnats.Observation]) (devices.Observation, error) {
	observationID, err := devices.ParseObservationID(envelope.ID)
	if err != nil {
		return devices.Observation{}, fmt.Errorf("parse Observation ID: %w", err)
	}
	entityID, err := devices.ParseEntityID(envelope.Data.EntityID)
	if err != nil {
		return devices.Observation{}, fmt.Errorf("parse Entity ID: %w", err)
	}
	adapterReceivedAt, err := time.Parse(time.RFC3339Nano, envelope.Data.AdapterReceivedAt)
	if err != nil {
		return devices.Observation{}, fmt.Errorf("parse adapter_received_at: %w", err)
	}
	observation := devices.Observation{
		ID: observationID, EntityID: entityID, Value: devices.Value(append([]byte(nil), envelope.Data.Value...)),
		AdapterReceivedAt: adapterReceivedAt,
	}
	if envelope.Data.SourceUpdatedAt != nil {
		sourceUpdatedAt, err := time.Parse(time.RFC3339Nano, *envelope.Data.SourceUpdatedAt)
		if err != nil {
			return devices.Observation{}, fmt.Errorf("parse source_updated_at: %w", err)
		}
		observation.SourceUpdatedAt = &sourceUpdatedAt
	}
	if envelope.Data.RefreshForCommand != nil {
		commandID, err := devices.ParseCommandID(*envelope.Data.RefreshForCommand)
		if err != nil {
			return devices.Observation{}, fmt.Errorf("parse refresh Command ID: %w", err)
		}
		observation.RefreshForCommand = &commandID
	}
	return observation, nil
}
