package nats

import "encoding/json"

type Envelope[T any] struct {
	ID            string  `json:"id"`
	Schema        string  `json:"schema"`
	EmittedAt     string  `json:"emitted_at"`
	CorrelationID string  `json:"correlation_id"`
	CausationID   *string `json:"causation_id,omitempty"`
	Data          T       `json:"data"`
}

type Registration struct {
	BindingKey string             `json:"binding_key"`
	Device     DeviceDescriptor   `json:"device"`
	Entities   []EntityDescriptor `json:"entities"`
}

type DeviceDescriptor struct {
	ExternalID *string `json:"external_id,omitempty"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
}

type EntityDescriptor struct {
	Key                 string          `json:"key"`
	ExternalID          string          `json:"external_id"`
	Name                string          `json:"name"`
	Type                string          `json:"type"`
	Constraints         json.RawMessage `json:"constraints"`
	SupportedOperations []string        `json:"operations"`
}

type Binding struct {
	BindingKey string          `json:"binding_key"`
	DeviceID   string          `json:"device_id"`
	Entities   []EntityBinding `json:"entities"`
}

type EntityBinding struct {
	Key      string `json:"key"`
	EntityID string `json:"entity_id"`
}

type RegistrationResponse struct {
	Status  string             `json:"status"`
	Binding *Binding           `json:"binding,omitempty"`
	Error   *RegistrationError `json:"error,omitempty"`
}

type RegistrationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Observation struct {
	EntityID          string          `json:"entity_id"`
	Value             json.RawMessage `json:"value"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
	SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
	RefreshForCommand *string         `json:"refresh_for_command_id,omitempty"`
}

type Command struct {
	EntityID      string          `json:"entity_id"`
	OperationName string          `json:"operation"`
	Parameters    json.RawMessage `json:"parameters"`
	Deadline      string          `json:"deadline"`
}

type CommandResponse struct {
	CommandID string        `json:"command_id"`
	Status    string        `json:"status"`
	Error     *CommandError `json:"error,omitempty"`
}

type CommandError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
