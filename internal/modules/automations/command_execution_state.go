package automations

import (
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// CommandSuccess retains one ownership-verified successful Command result.
//
//sumtype:decl
type CommandSuccess interface{ isCommandSuccess() }

// SatisfiedCommand links the ownership-verified satisfied Command.
type SatisfiedCommand struct{ VerifiedCommandID devices.CommandID }

// DispatchedCommand links the ownership-verified dispatched Command.
type DispatchedCommand struct{ VerifiedCommandID devices.CommandID }

func (SatisfiedCommand) isCommandSuccess()  {}
func (DispatchedCommand) isCommandSuccess() {}

// CommandReservation records identities before dispatch, without proving a Command exists.
type CommandReservation struct {
	CommandID     devices.CommandID
	CorrelationID devices.CorrelationID
}

// CommandExecutionState is the state of one Command execution.
//
//sumtype:decl
type CommandExecutionState interface{ isCommandExecutionState() }

// CommandTerminalState is an established terminal Command execution state.
//
//sumtype:decl
type CommandTerminalState interface {
	CommandExecutionState
	isCommandTerminalState()
}

type CommandRunning struct{ Reservation CommandReservation }

type CommandSucceeded struct {
	CompletedAt time.Time
	Reservation CommandReservation
	Outcome     CommandSuccess
}

type CommandFailed struct {
	CompletedAt       time.Time
	Reservation       CommandReservation
	FailureCode       string
	VerifiedCommandID *devices.CommandID
}

type CommandInterrupted struct {
	CompletedAt       time.Time
	Reservation       *CommandReservation
	FailureCode       string
	VerifiedCommandID *devices.CommandID
}

func (CommandRunning) isCommandExecutionState()     {}
func (CommandSucceeded) isCommandExecutionState()   {}
func (CommandFailed) isCommandExecutionState()      {}
func (CommandInterrupted) isCommandExecutionState() {}
func (CommandSucceeded) isCommandTerminalState()    {}
func (CommandFailed) isCommandTerminalState()       {}
func (CommandInterrupted) isCommandTerminalState()  {}

// ValidateCommandExecutionState validates an independently supplied Command state.
func ValidateCommandExecutionState(state CommandExecutionState) error {
	switch s := state.(type) {
	case CommandRunning:
		return validateCommandReservation(s.Reservation)
	case CommandSucceeded:
		return validateCommandSucceeded(s)
	case CommandFailed:
		return validateCommandFailed(s)
	case CommandInterrupted:
		return validateCommandInterrupted(s)
	default:
		return invalid("invalid Command execution state %T", state)
	}
}

func validateCommandReservation(reservation CommandReservation) error {
	if _, err := devices.ParseCommandID(string(reservation.CommandID)); err != nil {
		return fmt.Errorf("%w: command reservation: %w", ErrInvalidAutomation, err)
	}
	if _, err := devices.ParseCorrelationID(string(reservation.CorrelationID)); err != nil {
		return fmt.Errorf("%w: command reservation: %w", ErrInvalidAutomation, err)
	}
	return nil
}

func validateVerifiedCommandID(id *devices.CommandID, reservation *CommandReservation) error {
	if id == nil {
		return nil
	}
	if _, err := devices.ParseCommandID(string(*id)); err != nil {
		return fmt.Errorf("%w: verified command ID: %w", ErrInvalidAutomation, err)
	}
	if reservation == nil || *id != reservation.CommandID {
		return invalid("verified Command does not match reservation")
	}
	return nil
}

func validateCommandSucceeded(state CommandSucceeded) error {
	if !isValidExecutionTimestamp(state.CompletedAt) {
		return invalid("invalid Command completion time")
	}
	if err := validateCommandReservation(state.Reservation); err != nil {
		return err
	}
	var verified *devices.CommandID
	switch outcome := state.Outcome.(type) {
	case SatisfiedCommand:
		verified = &outcome.VerifiedCommandID
	case DispatchedCommand:
		verified = &outcome.VerifiedCommandID
	default:
		return invalid("invalid Command success %T", state.Outcome)
	}
	return validateVerifiedCommandID(verified, &state.Reservation)
}

func validateCommandFailed(state CommandFailed) error {
	if !isValidExecutionTimestamp(state.CompletedAt) || state.FailureCode == "" {
		return invalid("failed Command requires completion time and reason")
	}
	if err := validateCommandReservation(state.Reservation); err != nil {
		return err
	}
	return validateVerifiedCommandID(state.VerifiedCommandID, &state.Reservation)
}

func validateCommandInterrupted(state CommandInterrupted) error {
	if !isValidExecutionTimestamp(state.CompletedAt) || state.FailureCode == "" {
		return invalid("interrupted Command requires completion time and reason")
	}
	if state.Reservation == nil {
		if state.VerifiedCommandID != nil || !isInterruptionCode(state.FailureCode) {
			return invalid("invalid unreserved Command interruption")
		}
		return nil
	}
	if err := validateCommandReservation(*state.Reservation); err != nil {
		return err
	}
	return validateVerifiedCommandID(state.VerifiedCommandID, state.Reservation)
}

// CommandStateCompletion returns the terminal timestamp, or nil for a running state.
func CommandStateCompletion(state CommandExecutionState) (*time.Time, error) {
	if err := ValidateCommandExecutionState(state); err != nil {
		return nil, err
	}
	switch s := state.(type) {
	case CommandRunning:
		// A running execution has no completion instant; nil is its valid optional result.
		//nolint:nilnil // nil timestamp with nil error represents an active state, not failure.
		return nil, nil
	case CommandSucceeded:
		return &s.CompletedAt, nil
	case CommandFailed:
		return &s.CompletedAt, nil
	case CommandInterrupted:
		return &s.CompletedAt, nil
	default:
		return nil, invalid("invalid Command execution state %T", state)
	}
}
