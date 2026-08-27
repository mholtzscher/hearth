package nats

import "encoding/json"

type registration struct {
	BindingKey string             `json:"binding_key"`
	Device     deviceDescriptor   `json:"device"`
	Entities   []entityDescriptor `json:"entities"`
}

type deviceDescriptor struct {
	ExternalID *string `json:"external_id,omitempty"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
}

type entityDescriptor struct {
	Key              string          `json:"key"`
	ExternalID       string          `json:"external_id"`
	Name             string          `json:"name"`
	Type             string          `json:"type"`
	Support          json.RawMessage `json:"support"`
	InitiallyEnabled *bool           `json:"initially_enabled,omitempty"`
}

type binding struct {
	BindingKey string          `json:"binding_key"`
	DeviceID   string          `json:"device_id"`
	Entities   []entityBinding `json:"entities"`
}

type entityBinding struct {
	Key      string `json:"key"`
	EntityID string `json:"entity_id"`
	Enabled  bool   `json:"enabled"`
}

type registrationResponse struct {
	Status  string             `json:"status"`
	Binding *binding           `json:"binding,omitempty"`
	Error   *registrationError `json:"error,omitempty"`
}

type registrationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type entityEnablementRequest struct {
	EntityID string `json:"entity_id"`
	Enabled  bool   `json:"enabled"`
}

type entityEnablementResponse struct {
	Status   string                 `json:"status"`
	EntityID string                 `json:"entity_id,omitempty"`
	Enabled  *bool                  `json:"enabled,omitempty"`
	Error    *entityEnablementError `json:"error,omitempty"`
}

type entityEnablementError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type observation struct {
	EntityID          string          `json:"entity_id"`
	Value             json.RawMessage `json:"value"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
	SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
	RefreshForCommand *string         `json:"refresh_for_command_id,omitempty"`
}

type command struct {
	EntityID      string          `json:"entity_id"`
	OperationName string          `json:"operation"`
	Parameters    json.RawMessage `json:"parameters"`
	Deadline      string          `json:"deadline"`
}

type commandResponse struct {
	CommandID string        `json:"command_id"`
	Status    string        `json:"status"`
	Error     *commandError `json:"error,omitempty"`
}

type commandError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
