package automations

import (
	"fmt"
	"time"
)

// DelayExecutionState is the closed set of reached Delay lifecycle states.
//
//sumtype:decl
type DelayExecutionState interface{ isDelayExecutionState() }

// DelayTerminalState marks a Delay state that has finished.
//
//sumtype:decl
type DelayTerminalState interface {
	DelayExecutionState
	isDelayTerminalState()
}

type DelayRunning struct{}
type DelaySucceeded struct{ CompletedAt time.Time }
type DelayInterrupted struct {
	CompletedAt time.Time
	FailureCode string
}

func (DelayRunning) isDelayExecutionState()     {}
func (DelaySucceeded) isDelayExecutionState()   {}
func (DelayInterrupted) isDelayExecutionState() {}
func (DelaySucceeded) isDelayTerminalState()    {}
func (DelayInterrupted) isDelayTerminalState()  {}

// Validate checks intrinsic Delay execution evidence without encoding it.
func (details DelayExecutionDetails) Validate() error {
	if err := ValidateDelayExecutionState(details.State); err != nil {
		return err
	}
	if !isValidExecutionTimestamp(details.StartedAt) {
		return invalid("invalid execution start or kind")
	}
	return nil
}

// ValidateDelayExecutionState validates freely constructed lifecycle state.
func ValidateDelayExecutionState(state DelayExecutionState) error {
	switch state := state.(type) {
	case DelayRunning:
		return nil
	case DelaySucceeded:
		if isValidExecutionTimestamp(state.CompletedAt) {
			return nil
		}
	case DelayInterrupted:
		if isValidExecutionTimestamp(state.CompletedAt) && isInterruptionCode(state.FailureCode) {
			return nil
		}
	}
	return fmt.Errorf("%w: invalid delay execution state %T", ErrInvalidAutomation, state)
}

// DelayStateCompletion projects terminal time without inferring it from other evidence.
func DelayStateCompletion(state DelayExecutionState) (*time.Time, error) {
	if err := ValidateDelayExecutionState(state); err != nil {
		return nil, err
	}
	return delayStateCompletion(state), nil
}

// delayStateCompletion projects a validated Delay state.
func delayStateCompletion(state DelayExecutionState) *time.Time {
	switch state := state.(type) {
	case DelayRunning:
		return nil
	case DelaySucceeded:
		at := state.CompletedAt
		return &at
	case DelayInterrupted:
		at := state.CompletedAt
		return &at
	default:
		panic("unsupported validated Delay state")
	}
}
