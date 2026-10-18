package api

type AdapterHealthReasonBody struct {
	Code string `json:"code"`
}

type AdapterBody struct {
	ID     string            `json:"id"`
	Health AdapterHealthBody `json:"health"`
}

type AdapterHealthBody struct {
	Status           string                      `json:"status"`
	Source           string                      `json:"source"`
	Since            string                      `json:"since"`
	EvidenceAt       string                      `json:"evidence_at"`
	SourceObservedAt *string                     `json:"source_observed_at,omitempty"`
	Reason           *AdapterHealthReasonBody    `json:"reason,omitempty"`
	Runtime          *AdapterRuntimeEvidenceBody `json:"runtime,omitempty"`
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

type AdapterHealthTransitionBody struct {
	Status           string                   `json:"status"`
	Source           string                   `json:"source"`
	Reason           *AdapterHealthReasonBody `json:"reason,omitempty"`
	SourceObservedAt *string                  `json:"source_observed_at,omitempty"`
	ObservedAt       string                   `json:"observed_at"`
}

type AdapterCollectionBody struct {
	Items      []AdapterBody `json:"items"`
	NextCursor *string       `json:"next_cursor,omitempty"`
}

type AdapterHealthTransitionCollectionBody struct {
	Items      []AdapterHealthTransitionBody `json:"items"`
	NextCursor *string                       `json:"next_cursor,omitempty"`
}
