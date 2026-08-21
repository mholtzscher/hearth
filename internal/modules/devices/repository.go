package devices

import (
	"context"
	"errors"
	"time"
)

var (
	errIdentityConflict    = errors.New("registration identity conflict")
	errImmutableTypeChange = errors.New("entity type is immutable")
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

type RegistrationRepository interface {
	RegisterBinding(context.Context, RegisterBindingParams) (Binding, error)
}

type CommandLedger interface {
	CreateCommand(context.Context, CommandRecord) error
	MarkCommandAccepted(context.Context, CommandID, time.Time) error
	CompleteCommand(context.Context, CommandCompletion) error
	InterruptActiveCommands(context.Context, time.Time) error
}
