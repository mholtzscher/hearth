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
	switch state := state.(type) {
	case DelayRunning:
		return nil, nil //nolint:nilnil // Running executions have no completion timestamp.
	case DelaySucceeded:
		at := state.CompletedAt
		return &at, nil
	case DelayInterrupted:
		at := state.CompletedAt
		return &at, nil
	default:
		return nil, fmt.Errorf("%w: unsupported delay execution state %T", ErrInvalidAutomation, state)
	}
}
