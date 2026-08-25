package nats

import (
	"context"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

type observationReceiverFunc func(context.Context, devices.ReceivedObservation) (devices.ObservationReceipt, error)

func (receive observationReceiverFunc) ReceiveObservation(ctx context.Context, observation devices.ReceivedObservation) (devices.ObservationReceipt, error) {
	return receive(ctx, observation)
}

func TestObservationHandlerMapsIDsTimesAndJSON(t *testing.T) {
	const (
		observationID = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab"
		entityID      = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
		commandID     = "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	)
	adapterReceivedAt := time.Date(2026, 8, 20, 12, 0, 0, 123, time.FixedZone("adapter", -5*60*60))
	sourceUpdatedAt := adapterReceivedAt.Add(-time.Minute)
	observedAt := adapterReceivedAt.Add(time.Second)
	value := []byte(`{"value":true}`)
	called := false
	handler := ObservationHandler(observationReceiverFunc(func(_ context.Context, received devices.ReceivedObservation) (devices.ObservationReceipt, error) {
		called = true
		if received.AdapterID != "simulator" || received.Observation.ID != observationID ||
			received.Observation.EntityID != entityID || string(received.Observation.Value) != string(value) ||
			received.Observation.RefreshForCommand == nil || *received.Observation.RefreshForCommand != commandID ||
			received.Observation.SourceUpdatedAt == nil || !received.Observation.SourceUpdatedAt.Equal(sourceUpdatedAt) ||
			!received.ObservedAt.Equal(observedAt) || received.ObservedAt.Location() != time.UTC {
			t.Fatalf("received = %#v", received)
		}
		return devices.ObservationReceipt{Disposition: devices.DispositionApplied}, nil
	}))
	sourceText := sourceUpdatedAt.Format(time.RFC3339Nano)
	commandText := commandID
	err := handler(context.Background(), platformnats.ObservationDelivery{
		Envelope: platformnats.Envelope[platformnats.Observation]{
			ID: observationID,
			Data: platformnats.Observation{
				EntityID: entityID, Value: value,
				AdapterReceivedAt: adapterReceivedAt.Format(time.RFC3339Nano),
				SourceUpdatedAt:   &sourceText, RefreshForCommand: &commandText,
			},
		},
		Route:      platformnats.ObservationRoute{AdapterID: "simulator", EntityID: entityID},
		ObservedAt: observedAt,
	})
	if err != nil || !called {
		t.Fatalf("called = %v, error = %v", called, err)
	}
	value[0] = '['
}
