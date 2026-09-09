package devices

import (
	"context"
	"errors"
	"time"
)

var (
	errIdentityConflict           = errors.New("registration identity conflict")
	errImmutableTypeChange        = errors.New("entity type is immutable")
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
	GetCommand(context.Context, CommandID) (CommandRecord, error)
	ListEntityCommands(context.Context, ListEntityCommandsParams) (Page[CommandRecord], error)
	ListEntityStateHistory(context.Context, ListEntityStateHistoryParams) (Page[EntityStateHistoryEntry], error)
}

type EnablementRepository interface {
	SetEntityEnabled(context.Context, SetEntityEnabledParams) (EntityWithState, error)
}

type ObservationRepository interface {
	ProjectObservation(context.Context, ProjectObservationParams) (ProjectionResult, error)
	DeleteExpiredObservations(context.Context, time.Time) error
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
