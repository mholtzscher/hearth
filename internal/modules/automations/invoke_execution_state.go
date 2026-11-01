package automations

import "time"

const (
	invokeChildFailedCode      = "automation_run_failed"
	invokeChildInterruptedCode = "automation_run_interrupted"
)

type InvokeExecutionDetails struct {
	StartedAt time.Time
	State     InvokeExecutionState
}

//sumtype:decl
type InvokeExecutionState interface{ isInvokeExecutionState() }
type InvokeRunning struct{ ChildRunID RunID }
type InvokeSucceeded struct {
	ChildRunID  RunID
	CompletedAt time.Time
}
type InvokeRejected struct {
	CompletedAt time.Time
	ReasonCode  string
	SkipID      *SkipID
}
type InvokeChildFailed struct {
	ChildRunID  RunID
	CompletedAt time.Time
	Outcome     RunOutcome
}
type InvokeInterrupted struct {
	ChildRunID  RunID
	CompletedAt time.Time
	ReasonCode  string
}

func (InvokeRunning) isInvokeExecutionState()          {}
func (InvokeSucceeded) isInvokeExecutionState()        {}
func (InvokeRejected) isInvokeExecutionState()         {}
func (InvokeChildFailed) isInvokeExecutionState()      {}
func (InvokeInterrupted) isInvokeExecutionState()      {}
func (InvokeExecutionDetails) isStepExecutionDetails() {}

//nolint:gocognit // Validates the closed evidence variants independently.
func (d InvokeExecutionDetails) Validate() error {
	if !isValidExecutionTimestamp(d.StartedAt) {
		return invalid("invalid Invoke start time")
	}
	var child RunID
	var at time.Time
	switch s := d.State.(type) {
	case InvokeRunning:
		child = s.ChildRunID
	case InvokeSucceeded:
		child, at = s.ChildRunID, s.CompletedAt
	case InvokeRejected:
		at = s.CompletedAt
		switch s.ReasonCode {
		case "conditions_false", "conditions_unknown":
			if s.SkipID == nil {
				return invalid("Invoke rejection requires Skip")
			}
			if _, err := ParseSkipID(string(*s.SkipID)); err != nil {
				return err
			}
		case "automation_not_found", "automation_busy":
			if s.SkipID != nil {
				return invalid("Invoke rejection cannot carry Skip")
			}
		default:
			return invalid("invalid Invoke rejection")
		}
	case InvokeChildFailed:
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
	case InvokeInterrupted:
		child, at = s.ChildRunID, s.CompletedAt
		if !isInterruptionCode(s.ReasonCode) {
			return invalid("invalid Invoke interruption")
		}
	default:
		return invalid("invalid Invoke state")
	}
	if child != "" {
		if _, err := ParseRunID(string(child)); err != nil {
			return err
		}
	} else if _, ok := d.State.(InvokeRejected); !ok {
		return invalid("missing child Run ID")
	}
	if _, ok := d.State.(InvokeRunning); !ok && !isValidExecutionTimestamp(at) {
		return invalid("invalid Invoke completion time")
	}
	return nil
}
func (InvokeExecutionDetails) Kind() StepKind { return StepKindInvoke }
func (d InvokeExecutionDetails) Phase() string {
	switch d.State.(type) {
	case InvokeRunning:
		return executionRunning
	case InvokeSucceeded:
		return executionSucceeded
	case InvokeRejected, InvokeChildFailed:
		return executionFailed
	case InvokeInterrupted:
		return executionInterrupted
	default:
		panic("invalid Invoke state")
	}
}
func (d InvokeExecutionDetails) StartTime() time.Time { return d.StartedAt }
func (d InvokeExecutionDetails) CompletionTime() *time.Time {
	switch s := d.State.(type) {
	case InvokeRunning:
		return nil
	case InvokeSucceeded:
		return &s.CompletedAt
	case InvokeRejected:
		return &s.CompletedAt
	case InvokeChildFailed:
		return &s.CompletedAt
	case InvokeInterrupted:
		return &s.CompletedAt
	default:
		panic("invalid Invoke state")
	}
}
func (d InvokeExecutionDetails) RunFailure() (RunOutcome, bool) {
	switch s := d.State.(type) {
	case InvokeRejected:
		return FailedRun{FailureCode: s.ReasonCode}, true
	case InvokeChildFailed:
		if _, ok := s.Outcome.(InterruptedRun); ok {
			return FailedRun{FailureCode: invokeChildInterruptedCode}, true
		}
		return FailedRun{FailureCode: invokeChildFailedCode}, true
	case InvokeInterrupted:
		return InterruptedRun{FailureCode: s.ReasonCode}, true
	case InvokeRunning, InvokeSucceeded:
		return nil, false
	default:
		return nil, false
	}
}

// InvokeStopped records a local interruption without asserting a child outcome.
type InvokeStopped struct {
	At         time.Time
	ReasonCode string
}

func (InvokeStopped) isCompletedStepEvidence() {}
