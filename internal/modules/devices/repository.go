package devices

import (
	"context"
	"errors"
	"time"
)

var (
	errIdentityConflict    = errors.New("registration identity conflict")
	errImmutableTypeChange = errors.New("entity type is immutable")
	ErrDeviceNotFound      = errors.New("device not found")
	ErrEntityNotFound      = errors.New("entity not found")
	ErrCommandNotFound     = errors.New("command not found")
	ErrInvalidPage         = errors.New("invalid page")
	ErrCommandTerminal     = errors.New("command is already terminal")
	ErrInvalidCommand      = errors.New("invalid command")
	ErrAdapterUnavailable  = errors.New("adapter unavailable")
	ErrUpstreamRejected    = errors.New("upstream rejected")
	ErrOutcomeTimeout      = errors.New("command outcome timeout")
	ErrEntityDisabled      = errors.New("entity disabled")
	ErrEntityWrongAdapter  = errors.New("entity belongs to another adapter")
)

type RegisterEntityParams struct {
	EntityID EntityID
	Entity   EntityDescriptor
}

type RegisterBindingParams struct {
	AdapterID  string
	BindingKey string
	DeviceID   DeviceID
	Device     DeviceDescriptor
	Entities   []RegisterEntityParams
	UpdatedAt  time.Time
}

type SetEntityEnabledParams struct {
	EntityID      EntityID
	Enabled       bool
	RequiredOwner *string
	UpdatedAt     time.Time
}

type ProjectObservationParams struct {
	AdapterID        string
	Observation      Observation
	ObservedAt       time.Time
	Now              func() time.Time
	ReceiptExpiresAt time.Time
}

type RegistrationRepository interface {
	RegisterBinding(context.Context, RegisterBindingParams) (Binding, error)
}

type Repository interface {
	RegistrationRepository
	CommandLedger
	ListDevices(context.Context, ListDevicesParams) (Page[Device], error)
	GetDevice(context.Context, GetDeviceParams) (DeviceAggregate, error)
	ListEntities(context.Context, ListEntitiesParams) (Page[EntityWithState], error)
	GetEntity(context.Context, EntityID) (EntityWithState, error)
	SetEntityEnabled(context.Context, SetEntityEnabledParams) (EntityWithState, error)
	GetCommand(context.Context, CommandID) (CommandRecord, error)
	ListEntityCommands(context.Context, ListEntityCommandsParams) (Page[CommandRecord], error)
	ProjectObservation(context.Context, ProjectObservationParams) (ProjectionResult, error)
	DeleteExpiredObservationReceipts(context.Context, time.Time) error
}

type CommandLedger interface {
	CreateCommand(context.Context, CommandRecord) (CommandRecord, error)
	MarkCommandAccepted(context.Context, CommandID, time.Time) error
	CompleteCommand(context.Context, CommandCompletion) error
	InterruptActiveCommands(context.Context, time.Time) error
}

type CommandSender interface {
	Send(context.Context, string, CommandRequest) (CommandAcceptance, error)
}
