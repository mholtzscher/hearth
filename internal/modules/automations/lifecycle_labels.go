package automations

import (
	"time"
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
