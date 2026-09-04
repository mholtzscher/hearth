package devices

import (
	"encoding/json"
	"time"
)

type DeviceID string
type EntityID string
type ObservationID string
type CommandID string
type CorrelationID string
type RuntimeID string

type DeviceKind string

const (
	DeviceKindLight  DeviceKind = "light"
	DeviceKindRelay  DeviceKind = "relay"
	DeviceKindSensor DeviceKind = "sensor"
)

type EntityTypeID string

type OperationName string

const OperationNameSet OperationName = "set"

type EntitySupport json.RawMessage
type Value json.RawMessage
type CommandParameters json.RawMessage

type Device struct {
	ID   DeviceID
	Kind DeviceKind
	Name string
}

type Entity struct {
	ID        EntityID
	DeviceID  DeviceID
	AdapterID string
	Name      string
	TypeID    EntityTypeID
	Support   EntitySupport
	Enabled   bool
}

type State struct {
	EntityID          EntityID
	Value             Value
	ObservationID     ObservationID
	AdapterReceivedAt time.Time
	SourceUpdatedAt   *time.Time
	ObservedAt        time.Time
	ReceiveOrder      int64
}

type EntityWithState struct {
	Entity       Entity
	State        *State
	Availability EntityAvailability
}

type DeviceAggregate struct {
	Device   Device
	Entities Page[EntityWithState]
}

type GetDeviceParams struct {
	ID            DeviceID
	AfterEntityID *EntityID
	EntityLimit   int
}

type ListDevicesParams struct {
	AfterID *DeviceID
	Limit   int
}

type ListEntitiesParams struct {
	DeviceID *DeviceID
	AfterID  *EntityID
	Limit    int
}

type ListEntityCommandsParams struct {
	EntityID          EntityID
	BeforeRequestedAt *time.Time
	BeforeID          *CommandID
	Limit             int
}

type Page[T any] struct {
	Items   []T
	HasMore bool
}

type Observation struct {
	ID                ObservationID
	EntityID          EntityID
	Value             Value
	AdapterReceivedAt time.Time
	SourceUpdatedAt   *time.Time
	RefreshForCommand *CommandID
}

type ObservationDisposition string

const (
	DispositionApplied   ObservationDisposition = "applied"
	DispositionUnchanged ObservationDisposition = "unchanged"
	DispositionRejected  ObservationDisposition = "rejected"
	DispositionDuplicate ObservationDisposition = "duplicate"
)

type ObservationRejection string

const (
	RejectionUnknownEntity  ObservationRejection = "unknown_entity"
	RejectionWrongAdapter   ObservationRejection = "wrong_adapter"
	RejectionEntityDisabled ObservationRejection = "entity_disabled"
	RejectionInvalidValue   ObservationRejection = "invalid_value"
	RejectionStaleRuntime   ObservationRejection = "stale_runtime"
)

type ProjectionResult struct {
	Disposition      ObservationDisposition
	State            *State
	Rejection        *ObservationRejection
	SatisfiedCommand *CommandResult
}

type CommandStatus string

const (
	CommandStatusRequested         CommandStatus = "requested"
	CommandStatusAccepted          CommandStatus = "accepted"
	CommandStatusSatisfied         CommandStatus = "satisfied"
	CommandStatusRejected          CommandStatus = "rejected"
	CommandStatusAdapterUnhealthy  CommandStatus = "adapter_unhealthy"
	CommandStatusEntityUnavailable CommandStatus = "entity_unavailable"
	CommandStatusOutcomeTimeout    CommandStatus = "outcome_timeout"
	CommandStatusEntityDisabled    CommandStatus = "entity_disabled"
	CommandStatusInternalFailure   CommandStatus = "internal_failure"
	CommandStatusInterrupted       CommandStatus = "interrupted"
)

type CommandFailureCode string

const (
	CommandFailureAdapterUnhealthy  CommandFailureCode = "adapter_unhealthy"
	CommandFailureEntityUnavailable CommandFailureCode = "entity_unavailable"
	CommandFailureUpstreamRejected  CommandFailureCode = "upstream_rejected"
	CommandFailureOutcomeTimeout    CommandFailureCode = "outcome_timeout"
	CommandFailureEntityDisabled    CommandFailureCode = "entity_disabled"
	CommandFailureInternalError     CommandFailureCode = "internal_error"
	CommandFailureCoreRestarted     CommandFailureCode = "core_restarted"
)

type CommandRequest struct {
	ID            CommandID
	CorrelationID CorrelationID
	EntityID      EntityID
	OperationName OperationName
	Parameters    CommandParameters
	Deadline      time.Time
}

type CommandAcceptance struct {
	Accepted bool
}

type CommandRecord struct {
	ID                   CommandID
	EntityID             EntityID
	AdapterID            string
	RuntimeID            *RuntimeID
	OperationName        OperationName
	Parameters           CommandParameters
	CorrelationID        CorrelationID
	Status               CommandStatus
	RequestedAt          time.Time
	DeadlineAt           time.Time
	AcceptedAt           *time.Time
	CompletedAt          *time.Time
	OutcomeObservationID *ObservationID
	FailureCode          *CommandFailureCode
}

type CommandCompletion struct {
	ID          CommandID
	Status      CommandStatus
	CompletedAt time.Time
	FailureCode CommandFailureCode
}

type CommandResult struct {
	CommandID     CommandID
	ObservationID ObservationID
	Value         Value
}
