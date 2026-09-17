package devices

import (
	"context"
	"errors"
	"time"
)

var (
	ErrCommandUnavailable         = errors.New("command admission is unavailable")
	ErrDeviceNotFound             = errors.New("device not found")
	ErrEntityNotFound             = errors.New("entity not found")
	ErrCommandNotFound            = errors.New("command not found")
	ErrCommandIDConflict          = errors.New("command ID already exists")
	ErrInvalidPage                = errors.New("invalid page")
	ErrCommandTerminal            = errors.New("command is already terminal")
	ErrInvalidCommand             = errors.New("invalid command")
	ErrAdapterNotFound            = errors.New("adapter not found")
	ErrAdapterActive              = errors.New("adapter already has an active runtime")
	ErrRuntimeClaimConflict       = errors.New("adapter runtime claim conflict")
	ErrInvalidHealthTransition    = errors.New("invalid adapter health transition")
	ErrInvalidAvailabilityRequest = errors.New("invalid entity availability request")
	ErrAdapterUnhealthy           = errors.New("adapter unhealthy")
	ErrEntityUnavailable          = errors.New("entity unavailable")
	ErrRuntimeFenced              = errors.New("adapter runtime fenced")
	ErrUpstreamRejected           = errors.New("upstream rejected")
	ErrOutcomeTimeout             = errors.New("command outcome timeout")
	ErrEntityDisabled             = errors.New("entity disabled")
	ErrEntityWrongAdapter         = errors.New("entity belongs to another adapter")
	ErrInvalidEntityEvent         = errors.New("invalid entity event")
	ErrInvalidDeviceFactLimit     = errors.New("invalid device fact limit")
)

type RegisterEntityParams struct {
	EntityID EntityID
	Entity   EntityDescriptor
}

type RegisterBindingParams struct {
	AdapterID  string
	RuntimeID  RuntimeID
	BindingKey string
	DeviceID   DeviceID
	Device     DeviceDescriptor
	Entities   []RegisterEntityParams
	UpdatedAt  time.Time
}

type SetEntityEnabledParams struct {
	EntityID        EntityID
	Enabled         bool
	RequiredOwner   *string
	RequiredRuntime *RuntimeID
	UpdatedAt       time.Time
}

type ProjectObservationParams struct {
	AdapterID   string
	RuntimeID   RuntimeID
	Observation Observation
	ObservedAt  time.Time
	Now         func() time.Time
}

type AdapterReader interface {
	GetAdapter(context.Context, string) (AdapterInstance, error)
}

type RegistrationRepository interface {
	AdapterReader
	RegisterBinding(context.Context, RegisterBindingParams) (Binding, error)
}

type OwnedMappingRepository interface {
	AdapterReader
	ListOwnedMappings(context.Context, ListOwnedMappingsParams) (Page[OwnedMapping], error)
}

type RuntimeRepository interface {
	ClaimAdapterRuntime(context.Context, ClaimRuntimeWrite) error
	RecordAdapterHeartbeat(context.Context, HeartbeatWrite) (HeartbeatResult, error)
	ReleaseAdapterRuntime(context.Context, ReleaseRuntimeWrite) error
	ExpireAdapterLeases(context.Context, ExpireLeasesWrite) error
}

type AdapterRepository interface {
	AdapterReader
	ListAdapters(context.Context, ListAdaptersParams) (Page[AdapterInstance], error)
	ListAdapterHealthHistory(context.Context, ListAdapterHealthParams) (Page[HealthTransition], error)
	ListEntityAvailabilityHistory(context.Context, ListEntityAvailabilityParams) (Page[HealthTransition], error)
}

type AvailabilityRepository interface {
	ReportEntityAvailability(context.Context, AvailabilityBatchWrite) (time.Time, error)
}

type ReadRepository interface {
	ListDevices(context.Context, ListDevicesParams) (Page[Device], error)
	GetDevice(context.Context, GetDeviceParams) (DeviceAggregate, error)
	ListEntities(context.Context, ListEntitiesParams) (Page[EntityWithState], error)
	GetEntity(context.Context, EntityID) (EntityWithState, error)
	GetEntityStateSnapshot(context.Context, []EntityID) (EntityStateSnapshot, error)
	GetCommand(context.Context, CommandID) (CommandRecord, error)
	ListEntityCommands(context.Context, ListEntityCommandsParams) (Page[CommandRecord], error)
	ListCommands(context.Context, ListCommandsParams) (Page[CommandRecord], error)
	ListEntityStateHistory(context.Context, ListEntityStateHistoryParams) (Page[EntityStateHistoryEntry], error)
}

type EnablementRepository interface {
	SetEntityEnabled(context.Context, SetEntityEnabledParams) (EntityWithState, error)
}

type ObservationRepository interface {
	ProjectObservation(context.Context, ProjectObservationParams) (ProjectionResult, error)
	DeleteExpiredObservations(context.Context, time.Time) error
}

// EntityEventRepository stores first-seen Entity Event reports and their
// disposition. The row doubles as history and as the duplicate guard, so a
// repeated event ID never creates a second row.
type EntityEventRepository interface {
	RecordEntityEvent(context.Context, RecordEntityEventParams) (EntityEventRecordResult, error)
	ListEntityEvents(context.Context, ListEntityEventsParams) (Page[EntityEventHistoryEntry], error)
	DeleteEntityEventsBefore(context.Context, time.Time, int) (int64, error)
}

// DeviceFactOutbox is the durable pending set of Device Facts the relay drains.
// Publication never stores a fact: a row exists only between the commit that
// accepted its evidence and the publication that consumed it, so
// ListPendingDeviceFacts returns exactly the unpublished work.
type DeviceFactOutbox interface {
	// ListPendingDeviceFacts returns up to limit pending facts in enqueue order,
	// oldest first, so a relay that deletes what it publishes makes progress
	// instead of re-reading the whole set. A stored row that cannot be decoded
	// must be reported as [DeviceFactRowError], which matches
	// [ErrInvalidDeviceFactRow]: the failure is permanent, so the relay faults and
	// preserves the row instead of retrying a decode that can never succeed.
	// Decoding stops at that row, so the returned slice is the valid older prefix
	// the relay must publish and delete before it faults; every row from the
	// poison row onward stays durable. Every other failure must stay an ordinary
	// retryable error and must return no prefix.
	ListPendingDeviceFacts(ctx context.Context, limit int) ([]PendingDeviceFact, error)
	// DeleteDeviceFact removes one published fact. Deleting a fact that is
	// already gone is not an error, so a relay cannot fail on work another drain
	// consumed.
	DeleteDeviceFact(ctx context.Context, factID DeviceFactID) error
}

// DeviceFactNotifier is the one nonblocking wake hint devices needs to hand
// durable pending work to the Device Fact relay.
//
// An implementation signals that new pending facts may exist. It must return
// promptly, must never perform network I/O, must never return an error and is
// allowed to lose a hint: the relay also polls the outbox, so a lost hint costs
// latency and never a fact. A nil notifier is a no-op, so focused tests and
// non-relay assembly need no implementation.
type DeviceFactNotifier interface {
	NotifyPendingDeviceFacts()
}

type CommandLedger interface {
	CreateCommand(context.Context, CommandRecord) (CommandRecord, error)
	MarkCommandAccepted(context.Context, CommandID, time.Time) error
	CompleteCommand(context.Context, CommandCompletion) error
	InterruptActiveCommands(context.Context, time.Time) error
}

type CommandSender interface {
	Send(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error)
}
