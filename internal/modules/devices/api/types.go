package api

type EntityBody struct {
	ID       string         `json:"id"`
	DeviceID string         `json:"device_id"`
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Support  map[string]any `json:"support"`
	State    *StateBody     `json:"state"`
}

type StateBody struct {
	// Huma uses this ignored marker to emit StateBody as an object-or-null schema.
	_                 struct{} `json:"-" nullable:"true"`
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
	ID       string       `json:"id"`
	Kind     string       `json:"kind"`
	Name     string       `json:"name"`
	Entities []EntityBody `json:"entities"`
}

type EntityCollectionBody struct {
	Items      []EntityBody `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

type DeviceCollectionBody struct {
	Items      []DeviceBody `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
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
