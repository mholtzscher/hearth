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
	adapterReceivedAt := time.Date(2026, 8, 24, 12, 0, 0, 123, time.FixedZone("adapter", -4*60*60))
	sourceUpdatedAt := adapterReceivedAt.Add(-time.Minute)
	observedAt := adapterReceivedAt.Add(time.Second)
	commandID := "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	value := []byte(`{"value":true}`)
	called := false
	handler := ObservationHandler(observationReceiverFunc(func(_ context.Context, received devices.ReceivedObservation) (devices.ObservationReceipt, error) {
		called = true
		if received.AdapterID != "simulator" || !received.ObservedAt.Equal(observedAt) || received.ObservedAt.Location() != time.UTC ||
			received.Observation.ID != "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab" ||
			received.Observation.EntityID != "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab" ||
			received.Observation.RefreshForCommand == nil || string(*received.Observation.RefreshForCommand) != commandID ||
			received.Observation.SourceUpdatedAt == nil || !received.Observation.SourceUpdatedAt.Equal(sourceUpdatedAt) ||
			string(received.Observation.Value) != string(value) {
			t.Fatalf("Received Observation = %#v", received)
		}
		return devices.ObservationReceipt{Disposition: devices.DispositionApplied}, nil
	}))
	err := handler(context.Background(), platformnats.ObservationDelivery{
		Route:      platformnats.ObservationRoute{AdapterID: "simulator", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"},
		ObservedAt: observedAt,
		Envelope: platformnats.Envelope[platformnats.Observation]{
			ID: "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			Data: platformnats.Observation{
				EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab", Value: value,
				AdapterReceivedAt: adapterReceivedAt.Format(time.RFC3339Nano),
				SourceUpdatedAt:   stringPointer(sourceUpdatedAt.Format(time.RFC3339Nano)),
				RefreshForCommand: &commandID,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Observation receiver was not called")
	}
	value[0] = 'x'
}

func stringPointer(value string) *string { return &value }
