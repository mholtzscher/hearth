package devices

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type DeviceID string
type EntityID string
type ObservationID string

// EntityEventID identifies one reported occurrence across all publication and
// delivery retries: redelivery of one ID is the same event, while a new ID is a
// new occurrence even when it repeats the same name.
type EntityEventID string

// EntityEventName is one named occurrence an Entity's type may report.
type EntityEventName string

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

type OutcomeKind string

const (
	OutcomeObserved   OutcomeKind = "observed"
	OutcomeDispatched OutcomeKind = "dispatched"
)

type OperationName string

const OperationNameSet OperationName = "set"

type EntitySupport json.RawMessage
type Value json.RawMessage
type CommandParameters json.RawMessage

// CommandInput permits internal callers to reserve command identity before execution.
// Empty identities are generated; supplied identities must be canonical and the Command ID fresh.
type CommandInput struct {
	ID            CommandID
	CorrelationID CorrelationID
	EntityID      EntityID
	OperationName OperationName
	Parameters    CommandParameters
}

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

// Observation is one inbound State report plus the Core-verified identities
// Core needs to describe the accepted commit. CorrelationID is the wire
// correlation Core copies into an accepted Observation fact; it is never
// persisted because facts are never reconstructed from history.
type Observation struct {
	ID                ObservationID
	EntityID          EntityID
	Value             Value
	CorrelationID     CorrelationID
	AdapterReceivedAt time.Time
	SourceUpdatedAt   *time.Time
	RefreshForCommand *CommandID
	// Trace is the inbound W3C trace context the report carried, retained with
	// any Device Fact this Observation produces so publication continues the
	// originating trace.
	Trace DeviceFactTraceContext
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
	// PendingFactID is the stable identity of the Device Fact this transaction
	// durably queued, or nil when the outcome is not eligible evidence
	// (rejected or duplicate) and nothing was queued. It is a domain signal for
	// the fact relay, not part of any wire response.
	PendingFactID *DeviceFactID
}

type CommandStatus string

const (
	CommandStatusRequested         CommandStatus = "requested"
	CommandStatusAccepted          CommandStatus = "accepted"
	CommandStatusSatisfied         CommandStatus = "satisfied"
	CommandStatusDispatched        CommandStatus = "dispatched"
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
	Outcome       OutcomeKind
	ObservationID *ObservationID // non-nil iff Outcome is observed
	Value         *Value         // non-nil iff Outcome is observed (Value is json.RawMessage)
}

// NewCommandResult enforces the outcome invariant at both completion sites:
// observed requires both pointers non-nil; dispatched requires both nil; any
// other outcome value is rejected.
func NewCommandResult(
	commandID CommandID,
	outcome OutcomeKind,
	observationID *ObservationID,
	value *Value,
) (CommandResult, error) {
	switch outcome {
	case OutcomeObserved:
		if observationID == nil || value == nil {
			return CommandResult{}, errors.New("observed command result requires an observation ID and value")
		}
	case OutcomeDispatched:
		if observationID != nil || value != nil {
			return CommandResult{}, errors.New("dispatched command result carries no observation or value")
		}
	default:
		return CommandResult{}, fmt.Errorf("invalid command result outcome %q", outcome)
	}
	return CommandResult{
		CommandID: commandID, Outcome: outcome, ObservationID: observationID, Value: value,
	}, nil
}
