package devices

import (
	"context"
	"errors"
	"time"
)

var (
	errIdentityConflict    = errors.New("registration identity conflict")
	errImmutableTypeChange = errors.New("entity type is immutable")
	ErrEntityNotFound      = errors.New("entity not found")
	ErrCommandNotFound     = errors.New("command not found")
	ErrCommandTerminal     = errors.New("command is already terminal")
)

type RegisterBindingParams struct {
	AdapterID  string
	BindingKey string
	DeviceID   DeviceID
	EntityID   EntityID
	Device     DeviceDescriptor
	Entity     EntityDescriptor
	UpdatedAt  time.Time
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
	GetEntityView(context.Context, EntityID) (EntityView, error)
	ProjectObservation(context.Context, ProjectObservationParams) (ProjectionResult, error)
	DeleteExpiredObservationReceipts(context.Context, time.Time) error
}

type CommandLedger interface {
	CreateCommand(context.Context, CommandRecord) error
	MarkCommandAccepted(context.Context, CommandID, time.Time) error
	CompleteCommand(context.Context, CommandCompletion) error
	InterruptActiveCommands(context.Context, time.Time) error
}
