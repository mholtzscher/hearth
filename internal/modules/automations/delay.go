package automations

import (
	"fmt"
	"time"
)

// DelayStep waits for a fixed elapsed duration from 1 millisecond through 24 hours.
type DelayStep struct {
	DurationMS int64
}

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

func normalizeDelayStep(id StepID, input DelayStep) (Step, error) {
	if input.DurationMS < 1 || input.DurationMS > 86400000 {
		return Step{}, definitionIssue("/steps", "Delay duration_ms must be 1 to 86400000")
	}
	return Step{ID: id, Body: input}, nil
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

// DelayDurationWithPreparedSnapshot resolves a delay using an unchanged prepared snapshot.
// The caller must prepare arbitrary definitions before using this lookup.
func DelayDurationWithPreparedSnapshot(snapshot Definition, id StepID) (int64, error) {
	step := findSnapshotStep(snapshot.Steps, id)
	if step == nil {
		return 0, fmt.Errorf("%w: snapshot Step %q is not a delay", ErrInvalidAutomation, id)
	}
	body, ok := step.Body.(DelayStep)
	if !ok {
		return 0, fmt.Errorf("%w: snapshot Step %q is not a delay", ErrInvalidAutomation, id)
	}
	return body.DurationMS, nil
}
