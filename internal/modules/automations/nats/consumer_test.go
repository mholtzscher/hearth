package nats //nolint:testpackage // Tests exercise package-private consumer lifecycle.

import (
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// observationWire builds one canonical Observation fact fixture with the
// supplied fact identity, so a test can publish distinct facts in one stream.
func observationWire(
	t *testing.T,
	validator *contractsv1.Validator,
	factID string,
) testDeviceFactMessage {
	t.Helper()
	input := defaultObservationFactInput()
	input.factID = factID
	input.messageID = factID
	return observationFactMessage(t, validator, input)
}

// assertDeviceFactReceiver checks service compatibility with the consumer's admission interface.
func assertDeviceFactReceiver(DeviceFactReceiver) {}

// The service must implement the transport's admission interface.
func TestAutomationServiceSatisfiesDeviceFactReceiver(t *testing.T) {
	t.Parallel()
	assertDeviceFactReceiver((*automations.Service)(nil))
}

// assertObservationFact checks mapped Observation identity, Entity, and family.
func assertObservationFact(
	t *testing.T,
	fact automations.DeviceFact,
	factID string,
	observationID string,
	entityID string,
) {
	t.Helper()
	observation, ok := fact.(automations.ObservationFact)
	if !ok {
		t.Fatalf("mapped family = %#v, want an observation", fact)
	}
	switch {
	case observation.FactID != devices.DeviceFactID(factID):
		t.Fatalf("fact id = %q, want %q", observation.FactID, factID)
	case observation.ObservationID != devices.ObservationID(observationID):
		t.Fatalf("observation id = %q, want %q", observation.ObservationID, observationID)
	case observation.EntityID != devices.EntityID(entityID):
		t.Fatalf("entity id = %q, want %q", observation.EntityID, entityID)
	case observation.Disposition != devices.DispositionApplied:
		t.Fatalf("disposition = %q, want applied", observation.Disposition)
	}
}

// assertEntityEventFact asserts one mapped Entity Event fact's identity.
func assertEntityEventFact(
	t *testing.T,
	fact automations.DeviceFact,
	factID string,
	eventID string,
	entityID string,
) {
	t.Helper()
	event, ok := fact.(automations.EntityEventFact)
	if !ok {
		t.Fatalf("mapped family = %#v, want an entity event", fact)
	}
	switch {
	case event.FactID != devices.DeviceFactID(factID):
		t.Fatalf("fact id = %q, want %q", event.FactID, factID)
	case event.EventID != devices.EntityEventID(eventID):
		t.Fatalf("event id = %q, want %q", event.EventID, eventID)
	case event.EntityID != devices.EntityID(entityID):
		t.Fatalf("entity id = %q, want %q", event.EntityID, entityID)
	case event.Name != devices.EntityEventName(testEventName):
		t.Fatalf("event name = %q, want %q", event.Name, testEventName)
	}
}
