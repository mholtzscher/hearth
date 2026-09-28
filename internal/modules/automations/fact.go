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
type DeviceFact struct {
	Family      DeviceFactFamily
	Observation *ObservationFact
	EntityEvent *EntityEventFact
}

// DeviceFactSummary is immutable history evidence; the two value fields are
// present only for Observation Facts and PreviousStateValue may be JSON null.
type DeviceFactSummary struct {
	FactID             devices.DeviceFactID
	Family             DeviceFactFamily
	EntityID           devices.EntityID
	Variant            string // observation disposition or Entity Event name
	CausationID        string // obs_ or evt_
	ObservationValue   devices.Value
	PreviousStateValue devices.Value
	EmittedAt          time.Time
}

// ValidateDeviceFact rejects a Device Fact whose family payload is missing, contradictory, malformed, or unidentifiable.
func ValidateDeviceFact(fact DeviceFact) error {
	switch fact.Family {
	case DeviceFactObservation:
		if fact.Observation == nil || fact.EntityEvent != nil {
			return invalidFact("observation family payload mismatch")
		}
		return validateObservationFact(*fact.Observation)
	case DeviceFactEntityEvent:
		if fact.EntityEvent == nil || fact.Observation != nil {
			return invalidFact("entity event family payload mismatch")
		}
		return validateEntityEventFact(*fact.EntityEvent)
	default:
		return invalidFact("unknown family")
	}
}

// ValidateDeviceFactSummary rejects an impossible retained Fact summary.
func ValidateDeviceFactSummary(summary DeviceFactSummary) error {
	if err := validateDeviceFactSummaryIdentity(summary); err != nil {
		return err
	}
	switch summary.Family {
	case DeviceFactObservation:
		return validateObservationFactSummary(summary)
	case DeviceFactEntityEvent:
		return validateEntityEventFactSummary(summary)
	default:
		return invalid("fact summary: unknown family")
	}
}

func validateDeviceFactSummaryIdentity(summary DeviceFactSummary) error {
	if _, err := devices.ParseDeviceFactID(string(summary.FactID)); err != nil {
		return invalid("fact summary: %s", err)
	}
	if _, err := devices.ParseEntityID(string(summary.EntityID)); err != nil {
		return invalid("fact summary: %s", err)
	}
	if summary.EmittedAt.IsZero() {
		return invalid("fact summary: emit time is required")
	}
	return nil
}

func validateObservationFactSummary(summary DeviceFactSummary) error {
	if summary.ObservationValue == nil {
		return invalid("fact summary: observation value is required")
	}
	if summary.PreviousStateValue != nil {
		if _, err := decodeJSONValue(json.RawMessage(summary.PreviousStateValue)); err != nil {
			return invalid("fact summary: previous state value must be exactly one JSON value")
		}
	}
	if summary.Variant != string(devices.DispositionApplied) &&
		summary.Variant != string(devices.DispositionUnchanged) {
		return invalid("fact summary: observation variant %q is not applied or unchanged", summary.Variant)
	}
	if _, err := devices.ParseObservationID(summary.CausationID); err != nil {
		return invalid("fact summary: %s", err)
	}
	return nil
}

func validateEntityEventFactSummary(summary DeviceFactSummary) error {
	if summary.ObservationValue != nil || summary.PreviousStateValue != nil {
		return invalid("fact summary: entity event summary carries observation values")
	}
	if !subjectSlugPattern.MatchString(summary.Variant) {
		return invalid("fact summary: entity event name is not a subject-safe slug")
	}
	if _, err := devices.ParseEntityEventID(summary.CausationID); err != nil {
		return invalid("fact summary: %s", err)
	}
	return nil
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

// NewDeviceFactSummary copies one Device Fact into immutable history evidence so
// a retained Run or Skip stays explainable after Fact history is pruned.
func NewDeviceFactSummary(fact DeviceFact) DeviceFactSummary {
	summary := DeviceFactSummary{Family: fact.Family}
	switch fact.Family {
	case DeviceFactObservation:
		summary.FactID = fact.Observation.FactID
		summary.EntityID = fact.Observation.EntityID
		summary.Variant = string(fact.Observation.Disposition)
		summary.CausationID = string(fact.Observation.ObservationID)
		summary.ObservationValue = append(devices.Value(nil), fact.Observation.Value...)
		summary.PreviousStateValue = append(devices.Value(nil), fact.Observation.PreviousValue...)
		summary.EmittedAt = fact.Observation.EmittedAt
	case DeviceFactEntityEvent:
		summary.FactID = fact.EntityEvent.FactID
		summary.EntityID = fact.EntityEvent.EntityID
		summary.Variant = string(fact.EntityEvent.Name)
		summary.CausationID = string(fact.EntityEvent.EventID)
		summary.EmittedAt = fact.EntityEvent.EmittedAt
	}
	return summary
}
