package automations

import (
	"fmt"
	"time"
)

// DelayStep waits for a fixed elapsed duration from 1 millisecond through 24 hours.
type DelayStep struct {
	DurationMS int64
}

// DelayStatus is the durable state of a reached Delay Step.
type DelayStatus string

const (
	// DelayRunning marks a reached wait with no terminal evidence yet.
	DelayRunning DelayStatus = "running"
	// DelayCompleted marks an elapsed wait whose completion evidence committed.
	DelayCompleted DelayStatus = "completed"
	// DelayInterrupted marks a wait stopped by shutdown, restart, or executor fault.
	DelayInterrupted DelayStatus = "interrupted"
)

// DelayExecution projects reached wait evidence into history. DurationMS comes
// from the immutable Run snapshot; DueAt is diagnostic, not timer authority.
type DelayExecution struct {
	StepID      StepID
	Position    int // Contiguous reached-delay order, independent of Commands and branches.
	DurationMS  int64
	Status      DelayStatus
	StartedAt   time.Time
	DueAt       time.Time
	CompletedAt *time.Time
	FailureCode *string
}

// DelayStart records one reached wait before elapsed waiting can finish.
// Duration and due time are not stored; history derives them from the snapshot.
type DelayStart struct {
	RunID     RunID
	StepID    StepID
	Position  int
	StartedAt time.Time
}

// DelayCompletion records terminal wait evidence. Interrupted completion also
// interrupts the parent Run atomically at the persistence boundary.
type DelayCompletion struct {
	RunID       RunID
	StepID      StepID
	Status      DelayStatus // Only completed or interrupted.
	CompletedAt time.Time
	FailureCode *string
}

func normalizeDelayStep(input Step) (Step, error) {
	if input.Delay == nil || input.If != nil || input.Choose != nil ||
		input.EntityID != "" || input.OperationName != "" || input.Parameters != nil {
		return Step{}, definitionIssue("/steps", "Delay family payload mismatch")
	}
	if input.Delay.DurationMS < 1 || input.Delay.DurationMS > 86400000 {
		return Step{}, definitionIssue("/steps", "Delay duration_ms must be 1 to 86400000")
	}
	return Step{ID: input.ID, Kind: StepKindDelay, Delay: &DelayStep{DurationMS: input.Delay.DurationMS}}, nil
}

// ValidateDelayStart validates freely constructed reached-wait input.
func ValidateDelayStart(start DelayStart) error {
	if _, err := ParseRunID(string(start.RunID)); err != nil {
		return err
	}
	if _, err := ParseStepID(string(start.StepID)); err != nil {
		return err
	}
	if start.Position < 0 || start.Position >= 64 || !validDelayTimestamp(start.StartedAt) {
		return fmt.Errorf("%w: invalid delay position or start time", ErrInvalidAutomation)
	}
	return nil
}

// ValidateDelayCompletion validates terminal evidence without comparing wall chronology.
func ValidateDelayCompletion(completion DelayCompletion) error {
	if _, err := ParseRunID(string(completion.RunID)); err != nil {
		return err
	}
	if _, err := ParseStepID(string(completion.StepID)); err != nil {
		return err
	}
	if !validDelayTimestamp(completion.CompletedAt) {
		return fmt.Errorf("%w: delay completion requires a nonzero UTC time", ErrInvalidAutomation)
	}
	switch completion.Status {
	case DelayRunning:
		return fmt.Errorf("%w: delay completion cannot be running", ErrInvalidAutomation)
	case DelayCompleted:
		if completion.FailureCode == nil {
			return nil
		}
	case DelayInterrupted:
		if completion.FailureCode != nil {
			switch *completion.FailureCode {
			case "core_stopping", "core_restarted", "executor_fault":
				return nil
			}
		}
	}
	return fmt.Errorf("%w: invalid delay terminal evidence", ErrInvalidAutomation)
}

func validDelayTimestamp(at time.Time) bool {
	_, offset := at.Zone()
	return !at.IsZero() && offset == 0
}

// DelayDurationWithPreparedSnapshot resolves a delay using an unchanged prepared snapshot.
// The caller must prepare arbitrary definitions before using this lookup.
func DelayDurationWithPreparedSnapshot(snapshot Definition, id StepID) (int64, error) {
	step := findSnapshotStep(snapshot.Steps, id)
	if step == nil || step.Kind != StepKindDelay || step.Delay == nil {
		return 0, fmt.Errorf("%w: snapshot Step %q is not a delay", ErrInvalidAutomation, id)
	}
	return step.Delay.DurationMS, nil
}
