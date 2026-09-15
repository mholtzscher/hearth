package devices

import (
	"fmt"
	"time"
)

type AdapterHealthStatus string

const (
	AdapterHealthUnknown   AdapterHealthStatus = "unknown"
	AdapterHealthHealthy   AdapterHealthStatus = "healthy"
	AdapterHealthUnhealthy AdapterHealthStatus = "unhealthy"
)

// RuntimeStatusOnline and RuntimeStatusOffline are the runtime evidence status
// values [RuntimeEvidence] reports: a claimed runtime stays online until its row
// records an end, whether by graceful release or lease expiry.
const (
	RuntimeStatusOnline  = "online"
	RuntimeStatusOffline = "offline"
)

// adapterLeaseDuration is the fixed Adapter runtime lease a claim or heartbeat
// renews. The health supervisor is the only lease-expiry authority, so an active
// runtime may renew an elapsed lease before the supervisor commits expiry.
const adapterLeaseDuration = 15 * time.Second

type EntityAvailabilityStatus string

const (
	EntityAvailabilityUnknown     EntityAvailabilityStatus = "unknown"
	EntityAvailabilityAvailable   EntityAvailabilityStatus = "available"
	EntityAvailabilityUnavailable EntityAvailabilityStatus = "unavailable"
)

type HealthReason struct {
	Code string
}

// CopyHealthReason returns a detached copy of reason, or nil for a nil reason.
// It keeps a caller from changing a persisted or returned health reason through
// a shared pointer; persistence adapters hand back owned reasons through it.
func CopyHealthReason(reason *HealthReason) *HealthReason {
	if reason == nil {
		return nil
	}
	return &HealthReason{Code: reason.Code}
}

// AdapterActiveError reports that a runtime claim lost to another active
// runtime of the same Adapter. RetryAfter is when that runtime's lease expires,
// so a caller can wait for takeover instead of retrying immediately. It matches
// [ErrAdapterActive] for [errors.Is].
type AdapterActiveError struct {
	RetryAfter time.Time
}

func (err *AdapterActiveError) Error() string {
	return fmt.Sprintf("%s until %s", ErrAdapterActive, err.RetryAfter.Format(time.RFC3339Nano))
}

func (*AdapterActiveError) Unwrap() error {
	return ErrAdapterActive
}

type RuntimeEvidence struct {
	ID              RuntimeID
	Status          string
	SoftwareName    string
	SoftwareVersion string
	ClaimedAt       time.Time
	LastHeartbeatAt *time.Time
	LeaseExpiresAt  time.Time
}

type AdapterHealth struct {
	Status           AdapterHealthStatus
	Source           string
	Since            time.Time
	EvidenceAt       time.Time
	SourceObservedAt *time.Time
	Reason           *HealthReason
	Runtime          *RuntimeEvidence
}

type AdapterInstance struct {
	ID     string
	Health AdapterHealth
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

type ClaimAdapterRuntimeParams struct {
	AdapterID       string
	RuntimeID       RuntimeID
	SoftwareName    string
	SoftwareVersion string
}

type AdapterHeartbeat struct {
	AdapterID        string
	RuntimeID        RuntimeID
	ExternalStatus   AdapterHealthStatus
	SourceObservedAt time.Time
	Reason           *HealthReason
}

type ClaimRuntimeWrite struct {
	RuntimeID       RuntimeID
	AdapterID       string
	SoftwareName    string
	SoftwareVersion string
	ClaimedAt       time.Time
	LeaseExpiresAt  time.Time
}

type HeartbeatWrite struct {
	AdapterID        string
	RuntimeID        RuntimeID
	ExternalStatus   AdapterHealthStatus
	SourceObservedAt time.Time
	Reason           *HealthReason
	ReceivedAt       time.Time
	LeaseExpiresAt   time.Time
}

type HeartbeatResult struct {
	LeaseExpiresAt time.Time
}

type ReleaseRuntimeWrite struct {
	AdapterID  string
	RuntimeID  RuntimeID
	ReleasedAt time.Time
}

type ExpireLeasesWrite struct {
	ExpiresAt time.Time
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
	RuntimeID  RuntimeID
	Reports    []EntityAvailabilityReport
	ReportedAt time.Time
}

type ListAdaptersParams struct {
	AfterID *string
	Limit   int
}

type ListAdapterHealthParams struct {
	AdapterID          string
	BeforeReceiveOrder *int64
	Limit              int
}

type ListEntityAvailabilityParams struct {
	EntityID           EntityID
	BeforeReceiveOrder *int64
	Limit              int
}
