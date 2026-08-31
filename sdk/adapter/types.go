package adapter

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

type Config struct {
	AdapterID       string
	SoftwareName    string
	SoftwareVersion string
	NATSURL         string
	Logger          *slog.Logger
}

type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy   HealthStatus = "healthy"
	HealthUnhealthy HealthStatus = "unhealthy"
)

type HealthReport struct {
	Status           HealthStatus
	SourceObservedAt time.Time
	ReasonCode       string
	Detail           string
}

type EntityAvailabilityStatus string

const (
	AvailabilityAvailable   EntityAvailabilityStatus = "available"
	AvailabilityUnavailable EntityAvailabilityStatus = "unavailable"
)

type EntityAvailabilityReport struct {
	EntityID         string
	Status           EntityAvailabilityStatus
	SourceObservedAt time.Time
	ReasonCode       string
	Detail           string
}

type EntityMetadata struct {
	Key        string
	ExternalID string
	Name       string
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
	Key              string          `json:"key"`
	ExternalID       string          `json:"external_id"`
	Name             string          `json:"name"`
	Type             string          `json:"type"`
	Support          json.RawMessage `json:"support"`
	InitiallyEnabled *bool           `json:"initially_enabled,omitempty"`
}

type Binding struct {
	BindingKey string          `json:"binding_key"`
	DeviceID   string          `json:"device_id"`
	Entities   []EntityBinding `json:"entities"`
}

type EntityBinding struct {
	Key      string `json:"key"`
	EntityID string `json:"entity_id"`
	Enabled  bool   `json:"enabled"`
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

type EntityEnablementRequest struct {
	EntityID string `json:"entity_id"`
	Enabled  bool   `json:"enabled"`
}

type EntityEnablementResponse struct {
	Status   string                 `json:"status"`
	EntityID string                 `json:"entity_id,omitempty"`
	Enabled  *bool                  `json:"enabled,omitempty"`
	Error    *EntityEnablementError `json:"error,omitempty"`
}

type EntityEnablementError struct {
	Code    EntityEnablementRejectionCode `json:"code"`
	Message string                        `json:"message"`
}

type Observation struct {
	EntityID          string          `json:"entity_id"`
	Value             json.RawMessage `json:"value"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
	SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
	RefreshForCommand *string         `json:"refresh_for_command_id,omitempty"`
}

type ObservationID string

type Command struct {
	ID            string          `json:"-"`
	CorrelationID string          `json:"-"`
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

type CommandHandler func(context.Context, Command, Responder) error

type Responder interface {
	Accept() error
	Reject(message string) error
	RejectUnavailable(message string) error
}
