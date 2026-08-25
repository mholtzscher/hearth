package devices

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReceiveObservationAdvancesStateAndDeduplicates(t *testing.T) {
	service, database := newDeviceTestService(t, productionServiceControls())
	runDeviceTestService(t, service, testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		return CommandAcceptance{}, errors.New("unexpected delivery")
	}))
	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	observedAt := time.Now().UTC()
	first := newObservation(t, entityID, `true`, observedAt.Add(-time.Minute))
	receipt, err := service.ReceiveObservation(context.Background(), ReceivedObservation{
		AdapterID: "simulator", Observation: first, ObservedAt: observedAt,
	})
	if err != nil || receipt.Disposition != DispositionApplied {
		t.Fatalf("first receipt = %#v, error = %v", receipt, err)
	}
	second := newObservation(t, entityID, `true`, observedAt.Add(-time.Hour))
	receipt, err = service.ReceiveObservation(context.Background(), ReceivedObservation{
		AdapterID: "simulator", Observation: second, ObservedAt: observedAt.Add(time.Second),
	})
	if err != nil || receipt.Disposition != DispositionUnchanged {
		t.Fatalf("second receipt = %#v, error = %v", receipt, err)
	}
	duplicate := second
	duplicate.Value = Value(`false`)
	receipt, err = service.ReceiveObservation(context.Background(), ReceivedObservation{
		AdapterID: "other-adapter", Observation: duplicate, ObservedAt: observedAt.Add(2 * time.Second),
	})
	if err != nil || receipt.Disposition != DispositionDuplicate {
		t.Fatalf("duplicate receipt = %#v, error = %v", receipt, err)
	}
	view, err := service.GetEntity(context.Background(), entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == nil || view.State.ObservationID != second.ID || string(view.State.Value) != "true" || view.State.ReceiveOrder != 2 {
		t.Fatalf("view = %#v", view)
	}
	var count int
	if err := database.QueryRow("SELECT count(*) FROM observation_receipts").Scan(&count); err != nil || count != 2 {
		t.Fatalf("receipt count = %d, error = %v", count, err)
	}
}

func TestReceiveObservationDurablyRejectsIdentityAndValue(t *testing.T) {
	service, database := newDeviceTestService(t, productionServiceControls())
	runDeviceTestService(t, service, testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		return CommandAcceptance{}, errors.New("unexpected delivery")
	}))
	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tests := []struct {
		adapter     string
		observation Observation
		want        ObservationRejection
	}{
		{"simulator", newObservation(t, unknown, `true`, now), RejectionUnknownEntity},
		{"other-adapter", newObservation(t, binding.Entities[0].EntityID, `true`, now), RejectionWrongAdapter},
		{"simulator", newObservation(t, binding.Entities[0].EntityID, `1`, now), RejectionInvalidValue},
	}
	for _, test := range tests {
		receipt, err := service.ReceiveObservation(context.Background(), ReceivedObservation{
			AdapterID: test.adapter, Observation: test.observation, ObservedAt: now,
		})
		if err != nil || receipt.Disposition != DispositionRejected || receipt.Rejection == nil || *receipt.Rejection != test.want {
			t.Fatalf("receipt = %#v, error = %v", receipt, err)
		}
	}
	var count int
	if err := database.QueryRow("SELECT count(*) FROM observation_receipts").Scan(&count); err != nil || count != len(tests) {
		t.Fatalf("receipt count = %d, error = %v", count, err)
	}
}

func newObservation(t *testing.T, entityID EntityID, value string, adapterReceivedAt time.Time) Observation {
	t.Helper()
	id, err := NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return Observation{ID: id, EntityID: entityID, Value: Value(value), AdapterReceivedAt: adapterReceivedAt}
}
