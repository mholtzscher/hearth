package nats

import "encoding/json"

const (
	statusAccepted       = "accepted"
	statusRejected       = "rejected"
	runtimeFencedCode    = "runtime_fenced"
	runtimeFencedMessage = "Adapter runtime is fenced"
)

type adapterClaimRequest struct {
	AdapterID       string `json:"adapter_id"`
	SoftwareName    string `json:"software_name"`
	SoftwareVersion string `json:"software_version"`
}

type adapterClaimResponse struct {
	Status              string             `json:"status"`
	RuntimeID           string             `json:"runtime_id,omitempty"`
	HeartbeatIntervalMS int64              `json:"heartbeat_interval_ms,omitempty"`
	LeaseDurationMS     int64              `json:"lease_duration_ms,omitempty"`
	Error               *adapterClaimError `json:"error,omitempty"`
}

type adapterClaimError struct {
	Code       string  `json:"code"`
	Message    string  `json:"message"`
	RetryAfter *string `json:"retry_after,omitempty"`
}

type adapterHeartbeatRequest struct {
	ExternalSystem externalSystemHealth `json:"external_system"`
}

type externalSystemHealth struct {
	Status           string        `json:"status"`
	SourceObservedAt string        `json:"source_observed_at"`
	Reason           *healthReason `json:"reason,omitempty"`
}

type healthReason struct {
	Code   string  `json:"code"`
	Detail *string `json:"detail,omitempty"`
}

type adapterHeartbeatResponse struct {
	Status         string        `json:"status"`
	LeaseExpiresAt string        `json:"lease_expires_at,omitempty"`
	Error          *adapterError `json:"error,omitempty"`
}

type adapterReleaseRequest struct{}

type adapterReleaseResponse struct {
	Status string        `json:"status"`
	Error  *adapterError `json:"error,omitempty"`
}

type adapterError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type entityAvailabilityRequest struct {
	Entities []entityAvailabilityEntry `json:"entities"`
}

type entityAvailabilityEntry struct {
	EntityID         string        `json:"entity_id"`
	Status           string        `json:"status"`
	SourceObservedAt string        `json:"source_observed_at"`
	Reason           *healthReason `json:"reason,omitempty"`
}

type entityAvailabilityResponse struct {
	Status     string                   `json:"status"`
	ReportedAt string                   `json:"reported_at,omitempty"`
	Count      int                      `json:"count,omitempty"`
	Error      *entityAvailabilityError `json:"error,omitempty"`
}

type entityAvailabilityError struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	EntityID string `json:"entity_id,omitempty"`
}

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
