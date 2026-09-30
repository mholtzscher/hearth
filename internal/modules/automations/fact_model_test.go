package automations_test

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// A Fact must carry exactly the payload its family names.
func TestValidateDeviceFactRejectsMismatchedFamilies(t *testing.T) {
	t.Parallel()
	observationID, observationErr := devices.NewObservationID()
	if observationErr != nil {
		t.Fatal(observationErr)
	}
	factID, factErr := devices.NewDeviceFactID()
	if factErr != nil {
		t.Fatal(factErr)
	}
	observation := &automations.ObservationFact{
		FactID:        factID,
		ObservationID: observationID,
		EntityID:      newEntityID(t),
		Disposition:   devices.DispositionApplied,
		Value:         devices.Value(`true`),
		EmittedAt:     modelTestTime,
	}
	if err := automations.ValidateDeviceFact(automations.DeviceFact{
		Family: automations.DeviceFactObservation, Observation: observation,
	}); err != nil {
		t.Fatalf("valid observation fact rejected: %v", err)
	}
	mismatched := []automations.DeviceFact{
		{Family: automations.DeviceFactObservation},
		{
			Family:      automations.DeviceFactObservation,
			Observation: observation,
			EntityEvent: &automations.EntityEventFact{},
		},
		{Family: automations.DeviceFactEntityEvent, Observation: observation},
		{Family: automations.DeviceFactFamily("unknown")},
	}
	for _, fact := range mismatched {
		if err := automations.ValidateDeviceFact(fact); err == nil {
			t.Fatalf("mismatched fact %+v was accepted", fact)
		}
	}
}
