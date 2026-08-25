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

// ReceivedObservation combines Adapter ownership and the Core-assigned
// durable receive time with one Adapter-reported Observation.
type ReceivedObservation struct {
	AdapterID   string
	Observation Observation
	ObservedAt  time.Time
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

// ObservationReceipt describes only the durable disposition of a received
// Observation. State and Command effects remain behind Service.
type ObservationReceipt struct {
	Disposition ObservationDisposition
	Rejection   *ObservationRejection
}

type CommandDispatch struct {
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

type CommandResult struct {
	CommandID     CommandID
	ObservationID ObservationID
	Value         Value
}
