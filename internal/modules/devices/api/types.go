package api

type EntityBody struct {
	ID           string           `json:"id"`
	DeviceID     string           `json:"device_id"`
	AdapterID    string           `json:"adapter_id"`
	Name         string           `json:"name"`
	Type         string           `json:"type"`
	Support      map[string]any   `json:"support"`
	Enabled      bool             `json:"enabled"`
	Availability AvailabilityBody `json:"availability"`
	State        *StateBody       `json:"state"`
}

type AvailabilityBody struct {
	Status           string            `json:"status"`
	Source           string            `json:"source"`
	Since            string            `json:"since"`
	EvidenceAt       string            `json:"evidence_at"`
	SourceObservedAt *string           `json:"source_observed_at,omitempty"`
	Reason           *HealthReasonBody `json:"reason,omitempty"`
}

type HealthReasonBody struct {
	Code string `json:"code"`
}

type AdapterBody struct {
	ID     string            `json:"id"`
	Health AdapterHealthBody `json:"health"`
}

type AdapterHealthBody struct {
	Status         string                      `json:"status"`
	Since          string                      `json:"since"`
	EvidenceAt     string                      `json:"evidence_at"`
	Reason         *HealthReasonBody           `json:"reason,omitempty"`
	Runtime        *AdapterRuntimeEvidenceBody `json:"runtime,omitempty"`
	ExternalSystem *ExternalSystemEvidenceBody `json:"external_system,omitempty"`
}

type AdapterRuntimeEvidenceBody struct {
	ID              string  `json:"id"`
	Status          string  `json:"status"`
	SoftwareName    string  `json:"software_name"`
	SoftwareVersion string  `json:"software_version"`
	ClaimedAt       string  `json:"claimed_at"`
	LastHeartbeatAt *string `json:"last_heartbeat_at"`
	LeaseExpiresAt  string  `json:"lease_expires_at"`
}

type ExternalSystemEvidenceBody struct {
	Status           string            `json:"status"`
	SourceObservedAt string            `json:"source_observed_at"`
	EvidenceAt       string            `json:"evidence_at"`
	Reason           *HealthReasonBody `json:"reason,omitempty"`
}

type HealthTransitionBody struct {
	Status           string            `json:"status"`
	Source           string            `json:"source"`
	Reason           *HealthReasonBody `json:"reason,omitempty"`
	SourceObservedAt *string           `json:"source_observed_at,omitempty"`
	ObservedAt       string            `json:"observed_at"`
}

type StateBody struct {
	// Huma uses this ignored marker to emit StateBody as an object-or-null schema.
	_                 struct{} `json:"-"                           nullable:"true"`
	Value             any      `json:"value"`
	ObservationID     string   `json:"observation_id"`
	AdapterReceivedAt string   `json:"adapter_received_at"`
	SourceUpdatedAt   *string  `json:"source_updated_at,omitempty"`
	ObservedAt        string   `json:"observed_at"`
}

type DeviceBody struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type DeviceDetailBody struct {
	ID               string       `json:"id"`
	Kind             string       `json:"kind"`
	Name             string       `json:"name"`
	Entities         []EntityBody `json:"entities"`
	NextEntityCursor *string      `json:"next_entity_cursor,omitempty"`
}

type EntityCollectionBody struct {
	Items      []EntityBody `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

type DeviceCollectionBody struct {
	Items      []DeviceBody `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

type AdapterCollectionBody struct {
	Items      []AdapterBody `json:"items"`
	NextCursor *string       `json:"next_cursor,omitempty"`
}

type HealthTransitionCollectionBody struct {
	Items      []HealthTransitionBody `json:"items"`
	NextCursor *string                `json:"next_cursor,omitempty"`
}

type CommandRecordBody struct {
	ID                   string         `json:"id"`
	EntityID             string         `json:"entity_id"`
	Operation            string         `json:"operation"`
	Parameters           map[string]any `json:"parameters"`
	Status               string         `json:"status"`
	RequestedAt          string         `json:"requested_at"`
	DeadlineAt           string         `json:"deadline_at"`
	AcceptedAt           *string        `json:"accepted_at,omitempty"`
	CompletedAt          *string        `json:"completed_at,omitempty"`
	OutcomeObservationID *string        `json:"outcome_observation_id,omitempty"`
	FailureCode          *string        `json:"failure_code,omitempty"`
}

type CommandCollectionBody struct {
	Items      []CommandRecordBody `json:"items"`
	NextCursor *string             `json:"next_cursor,omitempty"`
}

type PatchEntityBody struct {
	Enabled bool `json:"enabled"`
}

type CommandBody struct {
	OperationName string         `json:"operation"`
	Parameters    map[string]any `json:"parameters"`
}

type CommandResultBody struct {
	CommandID     string `json:"command_id"`
	Status        string `json:"status"`
	ObservationID string `json:"observation_id"`
	Value         any    `json:"value"`
}
