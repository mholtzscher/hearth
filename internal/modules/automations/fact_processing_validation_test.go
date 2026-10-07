package automations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Reject malformed direct Service input before even the definition pre-read.
func TestReceiveDeviceFactValidationPrecedesRepositoryReads(t *testing.T) {
	t.Parallel()
	repo := &observingAdmissionRepository{}
	service := automations.NewService(repo, newScriptedDevices(), runtimeTestDependencies())
	_, err := service.ReceiveDeviceFact(context.Background(), nil)
	if !errors.Is(err, automations.ErrInvalidDeviceFact) {
		t.Fatalf("error = %v, want ErrInvalidDeviceFact", err)
	}
	if calls := repo.listEnabledCalls.Load(); calls != 0 {
		t.Fatalf("definition reads = %d, want zero", calls)
	}
}

func TestReceiveDeviceFactRejectsInvalidObservationJSONBeforeRepositoryReads(t *testing.T) {
	t.Parallel()
	repo := &observingAdmissionRepository{}
	service := automations.NewService(repo, newScriptedDevices(), runtimeTestDependencies())
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	fact := automations.ObservationFact{
		FactID: factID, ObservationID: observationID, EntityID: newEntityID(t),
		Disposition: devices.DispositionApplied, Value: devices.Value(`true false`), EmittedAt: time.Now(),
	}
	_, err = service.ReceiveDeviceFact(context.Background(), fact)
	if !errors.Is(err, automations.ErrInvalidDeviceFact) {
		t.Fatalf("error = %v, want ErrInvalidDeviceFact", err)
	}
	if calls := repo.listEnabledCalls.Load(); calls != 0 {
		t.Fatalf("definition reads = %d, want zero", calls)
	}
}
