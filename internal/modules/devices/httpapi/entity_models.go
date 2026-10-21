package httpapi

type EntityBody struct {
	AdapterName  string           `json:"adapter_name"`
	NameOverride *string          `json:"name_override" nullable:"true"`
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

type StateBody struct {
	// Huma uses this ignored marker to emit StateBody as an object-or-null schema.
	_                 struct{} `json:"-"                           nullable:"true"`
	Value             any      `json:"value"`
	ObservationID     string   `json:"observation_id"`
	AdapterReceivedAt string   `json:"adapter_received_at"`
	SourceUpdatedAt   *string  `json:"source_updated_at,omitempty"`
	ObservedAt        string   `json:"observed_at"`
}
