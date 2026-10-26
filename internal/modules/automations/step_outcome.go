package automations

import (
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// StepOutcome records a terminal Command outcome with its permitted evidence.
//
//sumtype:decl
type StepOutcome interface{ isStepOutcome() }

// SatisfiedStep links the ownership-verified satisfied Command.
type SatisfiedStep struct{ VerifiedCommandID devices.CommandID }

// DispatchedStep links the ownership-verified dispatched Command.
type DispatchedStep struct{ VerifiedCommandID devices.CommandID }

// FailedStep may precede ownership verification of the reserved Command.
type FailedStep struct {
	FailureCode       string
	VerifiedCommandID *devices.CommandID
}

// InterruptedStep retains only Command evidence verified before interruption.
type InterruptedStep struct {
	FailureCode       string
	VerifiedCommandID *devices.CommandID
}

func (SatisfiedStep) isStepOutcome()   {}
func (DispatchedStep) isStepOutcome()  {}
func (FailedStep) isStepOutcome()      {}
func (InterruptedStep) isStepOutcome() {}

// CommandReservation records identities before dispatch, without proving a Command exists.
type CommandReservation struct {
	CommandID     devices.CommandID
	CorrelationID devices.CorrelationID
}
