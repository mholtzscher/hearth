package automations

import (
	"encoding/json"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

type VariablesExecutionDetails struct {
	At     time.Time
	Values json.RawMessage
}
type StateExecutionDetails struct {
	At             time.Time
	Output         json.RawMessage
	ReadAt         time.Time
	Observation    *ObservationEvidence
	UnusableReason *ConditionUnknownReason
}
type ValueFailureExecutionDetails struct {
	StepKind StepKind
	At       time.Time
	Failure  ValueFailure
}

func (VariablesExecutionDetails) isStepExecutionDetails()    {}
func (StateExecutionDetails) isStepExecutionDetails()        {}
func (ValueFailureExecutionDetails) isStepExecutionDetails() {}
func (d VariablesExecutionDetails) Validate() error {
	if !isValidExecutionTimestamp(d.At) {
		return invalid("invalid assignment time")
	}
	return validateDataVariables(d.Values, true)
}
func (d StateExecutionDetails) Validate() error {
	if !isValidExecutionTimestamp(d.At) || !isValidExecutionTimestamp(d.ReadAt) {
		return invalid("invalid State time")
	}
	normalized, err := exprvalue.NormalizeEvidence(d.Output)
	if err != nil {
		return invalid("invalid State output")
	}
	if d.UnusableReason != nil {
		if *d.UnusableReason == "" || string(normalized) != "null" {
			return invalid("invalid State fallback")
		}
		switch *d.UnusableReason {
		case ConditionUnknownEntityMissing, ConditionUnknownStateMissing:
			if d.Observation != nil {
				return invalid("missing State has Observation")
			}
		case ConditionUnknownEvidenceInFuture, ConditionUnknownEvidenceExpired, ConditionUnknownPointerMissing:
			if d.Observation == nil {
				return invalid("State reason requires Observation")
			}
		case ConditionUnknownTypeMismatch:
			return invalid("invalid State reason")
		default:
			return invalid("invalid State reason")
		}
	} else if d.Observation == nil {
		return invalid("usable State requires Observation")
	}
	if d.Observation != nil {
		return validateObservationEvidence(*d.Observation)
	}
	return nil
}
func (d ValueFailureExecutionDetails) Validate() error {
	if !isValidExecutionTimestamp(d.At) {
		return invalid("invalid value failure time")
	}
	if d.Failure.ConditionID != "" {
		return invalid("nonbranch failure cannot name a Condition")
	}
	switch d.StepKind {
	case StepKindCommand, StepKindFunction, StepKindNotification, StepKindReadState, StepKindSetVariables:
	case StepKindIf, StepKindChoose, StepKindDelay:
		return invalid("invalid value failure Step kind")
	default:
		return invalid("invalid value failure Step kind")
	}
	return validateDataFailure(d.Failure)
}
func (VariablesExecutionDetails) Kind() StepKind                  { return StepKindSetVariables }
func (StateExecutionDetails) Kind() StepKind                      { return StepKindReadState }
func (d ValueFailureExecutionDetails) Kind() StepKind             { return d.StepKind }
func (VariablesExecutionDetails) Phase() string                   { return executionSucceeded }
func (StateExecutionDetails) Phase() string                       { return executionSucceeded }
func (ValueFailureExecutionDetails) Phase() string                { return executionFailed }
func (d VariablesExecutionDetails) StartTime() time.Time          { return d.At }
func (d StateExecutionDetails) StartTime() time.Time              { return d.At }
func (d ValueFailureExecutionDetails) StartTime() time.Time       { return d.At }
func (d VariablesExecutionDetails) CompletionTime() *time.Time    { return &d.At }
func (d StateExecutionDetails) CompletionTime() *time.Time        { return &d.At }
func (d ValueFailureExecutionDetails) CompletionTime() *time.Time { return &d.At }
func (VariablesExecutionDetails) RunFailure() (RunOutcome, bool)  { return nil, false }
func (StateExecutionDetails) RunFailure() (RunOutcome, bool)      { return nil, false }
func (d ValueFailureExecutionDetails) RunFailure() (RunOutcome, bool) {
	return FailedRun{FailureCode: d.Failure.FailureCode}, true
}
