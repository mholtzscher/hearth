package devices

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestObservationReceiptAndStateRollBackTogether(t *testing.T) {
	service, database := newDeviceTestService(t, productionServiceControls())
	runDeviceTestService(t, service, testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		return CommandAcceptance{}, errors.New("unexpected delivery")
	}))
	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TEMP TRIGGER fail_state_insert
		BEFORE INSERT ON entity_states
		BEGIN SELECT RAISE(ABORT, 'state failed'); END`); err != nil {
		t.Fatal(err)
	}
	observation := newObservation(t, binding.Entities[0].EntityID, `true`, time.Now().UTC())
	if _, err := service.ReceiveObservation(context.Background(), ReceivedObservation{
		AdapterID: "simulator", Observation: observation, ObservedAt: time.Now().UTC(),
	}); err == nil {
		t.Fatal("Observation unexpectedly committed through failing State write")
	}
	var receipts, states int
	if err := database.QueryRow("SELECT count(*) FROM observation_receipts WHERE observation_id = ?", observation.ID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT count(*) FROM entity_states WHERE entity_id = ?", observation.EntityID).Scan(&states); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 || states != 0 {
		t.Fatalf("rollback left receipts/states = %d/%d", receipts, states)
	}
}
