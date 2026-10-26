package automations

import (
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// CommandOutcome retains an Automation Command Step's terminal result and permitted evidence.
//
//sumtype:decl
type CommandOutcome interface{ isCommandOutcome() }

// SatisfiedCommand links the ownership-verified satisfied Command.
type SatisfiedCommand struct{ VerifiedCommandID devices.CommandID }

// DispatchedCommand links the ownership-verified dispatched Command.
type DispatchedCommand struct{ VerifiedCommandID devices.CommandID }

// FailedCommand may precede ownership verification of the reserved Command.
type FailedCommand struct {
	FailureCode       string
	VerifiedCommandID *devices.CommandID
}

// InterruptedCommand retains only Command evidence verified before interruption.
type InterruptedCommand struct {
	FailureCode       string
	VerifiedCommandID *devices.CommandID
}

func (SatisfiedCommand) isCommandOutcome()   {}
func (DispatchedCommand) isCommandOutcome()  {}
func (FailedCommand) isCommandOutcome()      {}
func (InterruptedCommand) isCommandOutcome() {}

// CommandReservation records identities before dispatch, without proving a Command exists.
type CommandReservation struct {
	CommandID     devices.CommandID
	CorrelationID devices.CorrelationID
}
