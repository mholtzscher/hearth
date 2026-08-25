package devices

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestReceiveObservationAdvancesStateByReceiveOrderAndDeduplicates(t *testing.T) {
	service, database := newRunningDeviceTestService(t, productionServiceControls(), acceptingTestDelivery())
	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	first := newObservation(t, entityID, `true`, observedAt.Add(-time.Minute))
	receipt, err := service.ReceiveObservation(context.Background(), ReceivedObservation{
		AdapterID: "simulator", Observation: first, ObservedAt: observedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Disposition != DispositionApplied {
		t.Fatalf("first receipt = %#v", receipt)
	}

	olderSource := observedAt.Add(-24 * time.Hour)
	second := newObservation(t, entityID, `true`, observedAt.Add(-2*time.Hour))
	second.SourceUpdatedAt = &olderSource
	receipt, err = service.ReceiveObservation(context.Background(), ReceivedObservation{
		AdapterID: "simulator", Observation: second, ObservedAt: observedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Disposition != DispositionUnchanged {
		t.Fatalf("same-value receipt = %#v", receipt)
	}

	redelivery := second
	redelivery.Value = Value(`false`)
	receipt, err = service.ReceiveObservation(context.Background(), ReceivedObservation{
		AdapterID: "simulator", Observation: redelivery, ObservedAt: observedAt.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Disposition != DispositionDuplicate {
		t.Fatalf("duplicate receipt = %#v", receipt)
	}

	view, err := service.GetEntity(context.Background(), entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == nil || view.State.ObservationID != second.ID || string(view.State.Value) != "true" ||
		view.State.ReceiveOrder <= 1 || !view.State.AdapterReceivedAt.Equal(second.AdapterReceivedAt) ||
		view.Entity.Name != "Power" || view.Entity.AdapterID != "simulator" {
		t.Fatalf("entity view = %#v", view)
	}
	assertReceiptCount(t, database, 2)
}

func TestReceiveObservationDurablyRejectsIdentityAndValueFailures(t *testing.T) {
	service, database := newRunningDeviceTestService(t, productionServiceControls(), acceptingTestDelivery())
	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	observedAt := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	unknownEntityID, err := NewEntityID()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		adapterID   string
		observation Observation
		want        ObservationRejection
	}{
		{"unknown entity", "simulator", newObservation(t, unknownEntityID, `true`, observedAt), RejectionUnknownEntity},
		{"wrong adapter", "other-adapter", newObservation(t, entityID, `true`, observedAt), RejectionWrongAdapter},
		{"invalid value", "simulator", newObservation(t, entityID, `1`, observedAt), RejectionInvalidValue},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt, err := service.ReceiveObservation(context.Background(), ReceivedObservation{
				AdapterID: test.adapterID, Observation: test.observation,
				ObservedAt: observedAt.Add(time.Duration(index) * time.Second),
			})
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Disposition != DispositionRejected || receipt.Rejection == nil || *receipt.Rejection != test.want {
				t.Fatalf("receipt = %#v", receipt)
			}
		})
	}
	assertReceiptCount(t, database, len(tests))
	view, err := service.GetEntity(context.Background(), entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != nil {
		t.Fatalf("rejected observation created state: %#v", view.State)
	}
}

func TestReceiptPruningPinsCurrentStateUntilItAdvances(t *testing.T) {
	service, database := newRunningDeviceTestService(t, productionServiceControls(), acceptingTestDelivery())
	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	first := newObservation(t, entityID, `false`, start)
	second := newObservation(t, entityID, `true`, start.Add(time.Hour))
	for _, received := range []ReceivedObservation{
		{AdapterID: "simulator", Observation: first, ObservedAt: start},
		{AdapterID: "simulator", Observation: second, ObservedAt: start.Add(time.Hour)},
	} {
		if _, err := service.ReceiveObservation(context.Background(), received); err != nil {
			t.Fatal(err)
		}
	}

	cutoff := start.Add(observationReceiptRetention + 2*time.Hour)
	if err := pruneExpiredObservationReceipts(context.Background(), database, cutoff); err != nil {
		t.Fatal(err)
	}
	assertReceiptIDs(t, database, []ObservationID{second.ID})

	third := newObservation(t, entityID, `false`, cutoff)
	if _, err := service.ReceiveObservation(context.Background(), ReceivedObservation{
		AdapterID: "simulator", Observation: third, ObservedAt: cutoff,
	}); err != nil {
		t.Fatal(err)
	}
	if err := pruneExpiredObservationReceipts(context.Background(), database, cutoff.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	assertReceiptIDs(t, database, []ObservationID{third.ID})
}

func TestReceiveObservationCancellationLeavesNoWrites(t *testing.T) {
	service, database := newRunningDeviceTestService(t, productionServiceControls(), acceptingTestDelivery())
	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.ReceiveObservation(ctx, ReceivedObservation{
		AdapterID:   "simulator",
		Observation: newObservation(t, binding.Entities[0].EntityID, `true`, time.Now().UTC()),
		ObservedAt:  time.Now().UTC(),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	assertReceiptCount(t, database, 0)
}

func newObservation(t *testing.T, entityID EntityID, value string, adapterReceivedAt time.Time) Observation {
	t.Helper()
	id, err := NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return Observation{ID: id, EntityID: entityID, Value: Value(value), AdapterReceivedAt: adapterReceivedAt}
}

func assertReceiptCount(t *testing.T, database *sql.DB, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow("SELECT count(*) FROM observation_receipts").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("receipt count = %d, want %d", got, want)
	}
}

func assertReceiptIDs(t *testing.T, database *sql.DB, want []ObservationID) {
	t.Helper()
	rows, err := database.Query("SELECT observation_id FROM observation_receipts ORDER BY receive_order")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []ObservationID
	for rows.Next() {
		var id ObservationID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("receipt IDs = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("receipt IDs = %v, want %v", got, want)
		}
	}
}
