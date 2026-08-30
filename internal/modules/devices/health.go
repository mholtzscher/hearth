package devices

import "time"

type AdapterHealthStatus string

const (
	AdapterHealthUnknown   AdapterHealthStatus = "unknown"
	AdapterHealthHealthy   AdapterHealthStatus = "healthy"
	AdapterHealthUnhealthy AdapterHealthStatus = "unhealthy"
)

type EntityAvailabilityStatus string

const (
	EntityAvailabilityUnknown     EntityAvailabilityStatus = "unknown"
	EntityAvailabilityAvailable   EntityAvailabilityStatus = "available"
	EntityAvailabilityUnavailable EntityAvailabilityStatus = "unavailable"
)

type HealthReason struct {
	Code   string
	Detail *string
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

type ExternalSystemEvidence struct {
	Status           AdapterHealthStatus
	SourceObservedAt time.Time
	EvidenceAt       time.Time
	Reason           *HealthReason
}

type AdapterHealth struct {
	Status         AdapterHealthStatus
	Since          time.Time
	EvidenceAt     time.Time
	Reason         *HealthReason
	Runtime        *RuntimeEvidence
	ExternalSystem *ExternalSystemEvidence
}

type AdapterInstance struct {
	ID         string
	ArchivedAt *time.Time
	Health     *AdapterHealth
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

type RuntimeClaim struct {
	RuntimeID         RuntimeID
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
}

type ClaimRuntimeWrite struct {
	ClaimID         string
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
	LeaseExpiresAt            time.Time
	RefreshEntityAvailability bool
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
	AdapterID  string
	RuntimeID  RuntimeID
	Reports    []EntityAvailabilityReport
	ReportedAt time.Time
}

type ListAdaptersParams struct {
	AfterID         *string
	Limit           int
	IncludeArchived bool
}

type ArchiveAdapterParams struct {
	AdapterID  string
	ArchivedAt time.Time
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
