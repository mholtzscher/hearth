package automations

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// DeviceFactFamily is the closed family of Device Fact evidence an automation admits.
type DeviceFactFamily string

const (
	// DeviceFactObservation identifies an Observation Fact.
	DeviceFactObservation DeviceFactFamily = "observation"
	// DeviceFactEntityEvent identifies an Entity Event Fact.
	DeviceFactEntityEvent DeviceFactFamily = "entity_event"
)

// ObservationFact reports a committed, applied or unchanged Observation.
// EmittedAt is the Fact envelope time, not the Observation time.
type ObservationFact struct {
	FactID        devices.DeviceFactID
	ObservationID devices.ObservationID
	EntityID      devices.EntityID
	Disposition   devices.ObservationDisposition
	Value         devices.Value
	PreviousValue devices.Value // nil means absent; bytes containing null are a real predecessor.
	EmittedAt     time.Time
}

// EntityEventFact reports a committed Entity Event.
// EmittedAt is the Fact envelope time, not the Entity Event time.
type EntityEventFact struct {
	FactID    devices.DeviceFactID
	EventID   devices.EntityEventID
	EntityID  devices.EntityID
	Name      devices.EntityEventName
	EmittedAt time.Time
}

// DeviceFact is exactly one typed Device Fact family payload.
//
//sumtype:decl
type DeviceFact interface{ isDeviceFact() }

func (ObservationFact) isDeviceFact() {}
func (EntityEventFact) isDeviceFact() {}

// ValidateDeviceFact rejects a Device Fact whose family payload is missing, contradictory, malformed, or unidentifiable.
func ValidateDeviceFact(fact DeviceFact) error {
	switch fact := fact.(type) {
	case ObservationFact:
		return validateObservationFact(fact)
	case EntityEventFact:
		return validateEntityEventFact(fact)
	default:
		return invalidFact("unknown family")
	}
}

func validateObservationFact(fact ObservationFact) error {
	if _, err := devices.ParseDeviceFactID(string(fact.FactID)); err != nil {
		return invalidFact(err.Error())
	}
	if _, err := devices.ParseObservationID(string(fact.ObservationID)); err != nil {
		return invalidFact(err.Error())
	}
	if _, err := devices.ParseEntityID(string(fact.EntityID)); err != nil {
		return invalidFact(err.Error())
	}
	if !acceptedObservationDisposition(fact.Disposition) {
		return invalidFact(fmt.Sprintf("disposition %q is not an accepted observation", fact.Disposition))
	}
	if len(fact.Value) == 0 {
		return invalidFact("observation value is required")
	}
	if _, err := decodeJSONValue(json.RawMessage(fact.Value)); err != nil {
		return invalidFact("observation value must be exactly one JSON value")
	}
	if fact.PreviousValue != nil {
		if _, err := decodeJSONValue(json.RawMessage(fact.PreviousValue)); err != nil {
			return invalidFact("previous observation value must be exactly one JSON value")
		}
	}
	if fact.EmittedAt.IsZero() {
		return invalidFact("emit time is required")
	}
	return nil
}

func validateEntityEventFact(fact EntityEventFact) error {
	if _, err := devices.ParseDeviceFactID(string(fact.FactID)); err != nil {
		return invalidFact(err.Error())
	}
	if _, err := devices.ParseEntityEventID(string(fact.EventID)); err != nil {
		return invalidFact(err.Error())
	}
	if _, err := devices.ParseEntityID(string(fact.EntityID)); err != nil {
		return invalidFact(err.Error())
	}
	if !subjectSlugPattern.MatchString(string(fact.Name)) {
		return invalidFact("event name is not a subject-safe slug")
	}
	if fact.EmittedAt.IsZero() {
		return invalidFact("emit time is required")
	}
	return nil
}
func invalidFact(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidDeviceFact, message)
}

// acceptedObservationDisposition reports whether one Observation disposition is
// durable Device Fact evidence; rejected and duplicate Observations are recorded
// history but never facts.
func acceptedObservationDisposition(disposition devices.ObservationDisposition) bool {
	switch disposition {
	case devices.DispositionApplied, devices.DispositionUnchanged:
		return true
	case devices.DispositionRejected, devices.DispositionDuplicate:
		return false
	default:
		return false
	}
}

// CloneDeviceFact owns the mutable Observation values in validated evidence.
func CloneDeviceFact(fact DeviceFact) DeviceFact {
	switch fact := fact.(type) {
	case ObservationFact:
		fact.Value = append(devices.Value(nil), fact.Value...)
		fact.PreviousValue = append(devices.Value(nil), fact.PreviousValue...)
		return fact
	case EntityEventFact:
		return fact
	default:
		return nil
	}
}
