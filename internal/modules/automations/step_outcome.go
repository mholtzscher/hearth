package automations

import (
	"time"

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

// CommandReservation is the private identity recorded before Command dispatch.
type CommandReservation struct {
	CommandID     devices.CommandID
	CorrelationID devices.CorrelationID
}

// StepAttemptState distinguishes unreached, running, and completed attempts.
//
//sumtype:decl
type StepAttemptState interface{ isStepAttemptState() }

// NotAttemptedStep records a leaf that execution has not reached.
type NotAttemptedStep struct{}

// RunningStep records the reservation committed before dispatch.
type RunningStep struct {
	StartedAt   time.Time
	Reservation CommandReservation
}

// CompletedStep permits interruption before a Command identity was reserved.
type CompletedStep struct {
	StartedAt   time.Time
	CompletedAt time.Time
	Reservation *CommandReservation
	Outcome     StepOutcome
}

func (NotAttemptedStep) isStepAttemptState() {}
func (RunningStep) isStepAttemptState()      {}
func (CompletedStep) isStepAttemptState()    {}
