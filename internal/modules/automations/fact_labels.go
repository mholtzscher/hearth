package automations

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// FactID derives the receipt and log identity from supported Fact evidence.
func FactID(fact DeviceFact) devices.DeviceFactID {
	switch fact := fact.(type) {
	case ObservationFact:
		return fact.FactID
	case EntityEventFact:
		return fact.FactID
	default:
		return ""
	}
}

// FactFamily derives the existing SQL and log family label.
func FactFamily(fact DeviceFact) DeviceFactFamily {
	switch fact.(type) {
	case ObservationFact:
		return DeviceFactObservation
	case EntityEventFact:
		return DeviceFactEntityEvent
	default:
		return ""
	}
}

// FactEmittedAt derives the envelope time used for live admission freshness.
func FactEmittedAt(fact DeviceFact) time.Time {
	switch fact := fact.(type) {
	case ObservationFact:
		return fact.EmittedAt
	case EntityEventFact:
		return fact.EmittedAt
	default:
		return time.Time{}
	}
}

// CauseSource derives the existing source label without duplicating provenance.
func CauseSource(cause AdmissionCause) RunSource {
	switch cause.(type) {
	case ManualCause:
		return RunSourceManual
	case DeviceFactCause:
		return RunSourceDeviceFact
	case HeldStateCause:
		return RunSourceHeldState
	case ScheduleCause:
		return RunSourceSchedule
	default:
		return ""
	}
}
