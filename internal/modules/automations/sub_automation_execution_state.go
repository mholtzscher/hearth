package automations

import "time"

const (
	subAutomationChildFailedCode      = "automation_run_failed"
	subAutomationChildInterruptedCode = "automation_run_interrupted"
)

type SubAutomationExecutionDetails struct {
	StartedAt time.Time
	State     SubAutomationExecutionState
}

//sumtype:decl
type SubAutomationExecutionState interface{ isSubAutomationExecutionState() }
type SubAutomationRunning struct{ ChildRunID RunID }
type SubAutomationSucceeded struct {
	ChildRunID  RunID
	CompletedAt time.Time
}
type SubAutomationRejected struct {
	CompletedAt time.Time
	ReasonCode  string
	SkipID      *SkipID
}
type SubAutomationChildFailed struct {
	ChildRunID  RunID
	CompletedAt time.Time
	Outcome     RunOutcome
}
type SubAutomationInterrupted struct {
	ChildRunID  RunID
	CompletedAt time.Time
	ReasonCode  string
}

func (SubAutomationRunning) isSubAutomationExecutionState()     {}
func (SubAutomationSucceeded) isSubAutomationExecutionState()   {}
func (SubAutomationRejected) isSubAutomationExecutionState()    {}
func (SubAutomationChildFailed) isSubAutomationExecutionState() {}
func (SubAutomationInterrupted) isSubAutomationExecutionState() {}
func (SubAutomationExecutionDetails) isStepExecutionDetails()   {}

//nolint:gocognit // Validates the closed evidence variants independently.
func (d SubAutomationExecutionDetails) Validate() error {
	if !isValidExecutionTimestamp(d.StartedAt) {
		return invalid("invalid SubAutomation start time")
	}
	var child RunID
	var at time.Time
	switch s := d.State.(type) {
	case SubAutomationRunning:
		child = s.ChildRunID
	case SubAutomationSucceeded:
		child, at = s.ChildRunID, s.CompletedAt
	case SubAutomationRejected:
		at = s.CompletedAt
		switch s.ReasonCode {
		case "conditions_false", "conditions_unknown":
			if s.SkipID == nil {
				return invalid("SubAutomation rejection requires Skip")
			}
			if _, err := ParseSkipID(string(*s.SkipID)); err != nil {
				return err
			}
		case "automation_not_found", "automation_busy":
			if s.SkipID != nil {
				return invalid("SubAutomation rejection cannot carry Skip")
			}
		default:
			return invalid("invalid SubAutomation rejection")
		}
	case SubAutomationChildFailed:
		child, at = s.ChildRunID, s.CompletedAt
		switch outcome := s.Outcome.(type) {
		case FailedRun:
			if outcome.FailureCode == "" {
				return invalid("missing child failure")
			}
		case InterruptedRun:
			if outcome.FailureCode == "" {
				return invalid("missing child interruption")
			}
		case SucceededRun:
			return invalid("invalid child outcome")
		default:
			return invalid("invalid child outcome")
		}
	case SubAutomationInterrupted:
		child, at = s.ChildRunID, s.CompletedAt
		if !isInterruptionCode(s.ReasonCode) {
			return invalid("invalid SubAutomation interruption")
		}
	default:
		return invalid("invalid SubAutomation state")
	}
	if child != "" {
		if _, err := ParseRunID(string(child)); err != nil {
			return err
		}
	} else if _, ok := d.State.(SubAutomationRejected); !ok {
		return invalid("missing child Run ID")
	}
	if _, ok := d.State.(SubAutomationRunning); !ok && !isValidExecutionTimestamp(at) {
		return invalid("invalid SubAutomation completion time")
	}
	return nil
}
func (SubAutomationExecutionDetails) Kind() StepKind { return StepKindSubAutomation }
func (d SubAutomationExecutionDetails) Phase() string {
	switch d.State.(type) {
	case SubAutomationRunning:
		return executionRunning
	case SubAutomationSucceeded:
		return executionSucceeded
	case SubAutomationRejected, SubAutomationChildFailed:
		return executionFailed
	case SubAutomationInterrupted:
		return executionInterrupted
	default:
		panic("invalid SubAutomation state")
	}
}
func (d SubAutomationExecutionDetails) StartTime() time.Time { return d.StartedAt }
func (d SubAutomationExecutionDetails) CompletionTime() *time.Time {
	switch s := d.State.(type) {
	case SubAutomationRunning:
		return nil
	case SubAutomationSucceeded:
		return &s.CompletedAt
	case SubAutomationRejected:
		return &s.CompletedAt
	case SubAutomationChildFailed:
		return &s.CompletedAt
	case SubAutomationInterrupted:
		return &s.CompletedAt
	default:
		panic("invalid SubAutomation state")
	}
}
func (d SubAutomationExecutionDetails) RunFailure() (RunOutcome, bool) {
	switch s := d.State.(type) {
	case SubAutomationRejected:
		return FailedRun{FailureCode: s.ReasonCode}, true
	case SubAutomationChildFailed:
		if _, ok := s.Outcome.(InterruptedRun); ok {
			return FailedRun{FailureCode: subAutomationChildInterruptedCode}, true
		}
		return FailedRun{FailureCode: subAutomationChildFailedCode}, true
	case SubAutomationInterrupted:
		return InterruptedRun{FailureCode: s.ReasonCode}, true
	case SubAutomationRunning, SubAutomationSucceeded:
		return nil, false
	default:
		return nil, false
	}
}

// SubAutomationStopped records a local interruption without asserting a child outcome.
type SubAutomationStopped struct {
	At         time.Time
	ReasonCode string
}

func (SubAutomationStopped) isCompletedStepEvidence() {}
