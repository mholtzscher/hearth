package devices

import (
	"errors"
	"fmt"
)

var (
	errIdentityConflict    = errors.New("registration identity conflict")
	errImmutableTypeChange = errors.New("entity type is immutable")
	errCommandNotFound     = errors.New("command not found")
	errCommandTerminal     = errors.New("command is already terminal")

	ErrServiceStopped          = errors.New("Device / Entity service stopped")
	ErrServiceAlreadyRun       = errors.New("Device / Entity service Run already called")
	ErrCommandDeliveryRequired = errors.New("Command delivery is required")

	ErrEntityNotFound     = errors.New("entity not found")
	ErrInvalidCommand     = errors.New("invalid command")
	ErrAdapterUnavailable = errors.New("adapter unavailable")
	ErrUpstreamRejected   = errors.New("upstream rejected")
	ErrOutcomeTimeout     = errors.New("command outcome timeout")
)

// CommandExecutionError identifies a Command that was durably created before
// its lifecycle failed.
type CommandExecutionError struct {
	CommandID CommandID
	Err       error
}

func (err *CommandExecutionError) Error() string {
	return fmt.Sprintf("command %s: %v", err.CommandID, err.Err)
}

func (err *CommandExecutionError) Unwrap() error { return err.Err }
