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

type DeviceKind string

const DeviceKindLight DeviceKind = "light"

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

type EntityView struct {
	Entity Entity
	State  *State
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
	RejectionUnknownEntity ObservationRejection = "unknown_entity"
	RejectionWrongAdapter  ObservationRejection = "wrong_adapter"
	RejectionInvalidValue  ObservationRejection = "invalid_value"
)

type ProjectionResult struct {
	Disposition      ObservationDisposition
	State            *State
	Rejection        *ObservationRejection
	SatisfiedCommand *CommandResult
}

type CommandStatus string

const (
	CommandStatusRequested          CommandStatus = "requested"
	CommandStatusAccepted           CommandStatus = "accepted"
	CommandStatusSatisfied          CommandStatus = "satisfied"
	CommandStatusRejected           CommandStatus = "rejected"
	CommandStatusAdapterUnavailable CommandStatus = "adapter_unavailable"
	CommandStatusOutcomeTimeout     CommandStatus = "outcome_timeout"
	CommandStatusInternalFailure    CommandStatus = "internal_failure"
	CommandStatusInterrupted        CommandStatus = "interrupted"
)

type CommandFailureCode string

const (
	CommandFailureAdapterUnavailable CommandFailureCode = "adapter_unavailable"
	CommandFailureUpstreamRejected   CommandFailureCode = "upstream_rejected"
	CommandFailureOutcomeTimeout     CommandFailureCode = "outcome_timeout"
	CommandFailureInternalError      CommandFailureCode = "internal_error"
	CommandFailureCoreRestarted      CommandFailureCode = "core_restarted"
)

type CommandRecord struct {
	ID                   CommandID
	EntityID             EntityID
	AdapterID            string
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
