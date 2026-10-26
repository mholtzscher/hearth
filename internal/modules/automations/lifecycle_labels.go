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

// CommandOutcomeStatus labels a terminal Command outcome, not the common execution phase.
func CommandOutcomeStatus(outcome CommandOutcome) StepStatus {
	switch outcome.(type) {
	case SatisfiedCommand:
		return StepSatisfied
	case DispatchedCommand:
		return StepDispatched
	case FailedCommand:
		return StepFailed
	case InterruptedCommand:
		return StepInterrupted
	default:
		return ""
	}
}

// CommandOutcomeEvidence returns only public, verified Command evidence.
func CommandOutcomeEvidence(outcome CommandOutcome) (*devices.CommandID, *string) {
	switch outcome := outcome.(type) {
	case SatisfiedCommand:
		return &outcome.VerifiedCommandID, nil
	case DispatchedCommand:
		return &outcome.VerifiedCommandID, nil
	case FailedCommand:
		return cloneCommandID(outcome.VerifiedCommandID), &outcome.FailureCode
	case InterruptedCommand:
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
