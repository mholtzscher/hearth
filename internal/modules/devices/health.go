package devices

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type EntityAvailabilityStatus string

const (
	EntityAvailabilityUnknown     EntityAvailabilityStatus = "unknown"
	EntityAvailabilityAvailable   EntityAvailabilityStatus = "available"
	EntityAvailabilityUnavailable EntityAvailabilityStatus = "unavailable"
)

type HealthReason struct{ Code string }

func CopyHealthReason(reason *HealthReason) *HealthReason {
	if reason == nil {
		return nil
	}
	return &HealthReason{Code: reason.Code}
}

type EntityAvailability struct {
	Status           EntityAvailabilityStatus
	Source           string
	Since            time.Time
	EvidenceAt       time.Time
	SourceObservedAt *time.Time
	Reason           *HealthReason
}

type HealthTransition struct {
	ReceiveOrder     int64
	Status           string
	Source           string
	Reason           *HealthReason
	SourceObservedAt *time.Time
	ObservedAt       time.Time
}

type EntityAvailabilityReport struct {
	EntityID         EntityID
	Status           EntityAvailabilityStatus
	SourceObservedAt time.Time
	Reason           *HealthReason
}

type AvailabilityBatchWrite struct {
	RequestID  string
	AdapterID  string
	RuntimeID  adapters.RuntimeID
	Reports    []EntityAvailabilityReport
	ReportedAt time.Time
}

type ListEntityAvailabilityParams struct {
	EntityID           EntityID
	BeforeReceiveOrder *int64
	Limit              int
}
