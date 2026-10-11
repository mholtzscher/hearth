package automations

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// RunOutcomeStatus derives the persisted and public terminal status.
func RunOutcomeStatus(outcome RunOutcome) RunStatus {
	switch outcome.(type) {
	case SucceededRun:
		return RunSucceeded
	case FailedRun:
		return RunFailed
	case InterruptedRun:
		return RunInterrupted
	default:
		return ""
	}
}

// RunOutcomeFailureCode returns an owned optional failure label.
func RunOutcomeFailureCode(outcome RunOutcome) *string {
	switch outcome := outcome.(type) {
	case SucceededRun:
		return nil
	case FailedRun:
		return &outcome.FailureCode
	case InterruptedRun:
		return &outcome.FailureCode
	default:
		return nil
	}
}

// RunStateStatus derives the lifecycle status.
func RunStateStatus(state RunState) RunStatus {
	switch state := state.(type) {
	case RunningRun:
		return RunRunning
	case CompletedRun:
		return RunOutcomeStatus(state.Outcome)
	default:
		return ""
	}
}

// RunStateCompletion returns the optional completion timestamp and failure code.
func RunStateCompletion(state RunState) (*time.Time, *string) {
	switch state := state.(type) {
	case RunningRun:
		return nil, nil
	case CompletedRun:
		return &state.CompletedAt, RunOutcomeFailureCode(state.Outcome)
	default:
		return nil, nil
	}
}

// StepOutcomeStatus derives the persisted and public terminal status.
func StepOutcomeStatus(outcome StepOutcome) StepStatus {
	switch outcome.(type) {
	case SatisfiedStep:
		return StepSatisfied
	case DispatchedStep:
		return StepDispatched
	case FailedStep:
		return StepFailed
	case InterruptedStep:
		return StepInterrupted
	default:
		return ""
	}
}

// StepOutcomeEvidence returns only public, verified Command evidence.
func StepOutcomeEvidence(outcome StepOutcome) (*devices.CommandID, *string) {
	switch outcome := outcome.(type) {
	case SatisfiedStep:
		return &outcome.VerifiedCommandID, nil
	case DispatchedStep:
		return &outcome.VerifiedCommandID, nil
	case FailedStep:
		return cloneCommandID(outcome.VerifiedCommandID), &outcome.FailureCode
	case InterruptedStep:
		return cloneCommandID(outcome.VerifiedCommandID), &outcome.FailureCode
	default:
		return nil, nil
	}
}

func cloneCommandID(id *devices.CommandID) *devices.CommandID {
	if id == nil {
		return nil
	}
	copyID := *id
	return &copyID
}

// StepAttemptStatus derives the lifecycle status without exposing reservations.
func StepAttemptStatus(state StepAttemptState) StepStatus {
	switch state := state.(type) {
	case NotAttemptedStep:
		return StepNotAttempted
	case RunningStep:
		return StepRunning
	case CompletedStep:
		return StepOutcomeStatus(state.Outcome)
	default:
		return ""
	}
}
