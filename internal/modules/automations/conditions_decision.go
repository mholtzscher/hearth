package automations

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// ConditionDecisionMode distinguishes retained evidence from deliberate non-evaluation.
type ConditionDecisionMode string

const (
	// ConditionDecisionNotConfigured marks a definition that omitted
	// Conditions.
	ConditionDecisionNotConfigured ConditionDecisionMode = "not_configured"
	// ConditionDecisionNotEvaluated marks an automatic stale or busy
	// Skip of a configured definition.
	ConditionDecisionNotEvaluated ConditionDecisionMode = "not_evaluated"
	// ConditionDecisionBypassed marks an explicit manual bypass.
	ConditionDecisionBypassed ConditionDecisionMode = "bypassed"
	// ConditionDecisionEvaluated marks an eligible admission whose
	// Conditions were evaluated.
	ConditionDecisionEvaluated ConditionDecisionMode = "evaluated"
)

// ConditionDecision is the immutable admission explanation retained with a Run
// or Skip. Exactly one of the four explanations exists; build one with
// [NotConfiguredDecision], [NotEvaluatedDecision], [BypassedDecision], or
// [EvaluatedDecision]. Codecs validate supported value representations and nested
// evidence independently; constructors establish only the decision envelope.
//
//sumtype:decl
type ConditionDecision interface {
	// DecisionMode reports which explanation this decision carries.
	DecisionMode() ConditionDecisionMode
	// BypassRequested reports whether the decision records an explicit manual
	// bypass; true if and only if the mode is bypassed.
	BypassRequested() bool
	// DecisionSnapshot returns the configured Condition tree, nil when the
	// definition omitted Conditions.
	DecisionSnapshot() *Condition
	// DecisionEvaluation returns the recorded evaluation, non-nil only for an
	// evaluated decision.
	DecisionEvaluation() *ConditionEvaluation
	isConditionDecision()
}

type notConfiguredDecision struct{}
type notEvaluatedDecision struct{ snapshot Condition }
type bypassedDecision struct{ snapshot Condition }
type evaluatedDecision struct {
	snapshot   Condition
	evaluation ConditionEvaluation
}

func (notConfiguredDecision) isConditionDecision() {}
func (notEvaluatedDecision) isConditionDecision()  {}
func (bypassedDecision) isConditionDecision()      {}
func (evaluatedDecision) isConditionDecision()     {}

func (notConfiguredDecision) DecisionMode() ConditionDecisionMode {
	return ConditionDecisionNotConfigured
}
func (notEvaluatedDecision) DecisionMode() ConditionDecisionMode {
	return ConditionDecisionNotEvaluated
}
func (bypassedDecision) DecisionMode() ConditionDecisionMode  { return ConditionDecisionBypassed }
func (evaluatedDecision) DecisionMode() ConditionDecisionMode { return ConditionDecisionEvaluated }

func (notConfiguredDecision) BypassRequested() bool { return false }
func (notEvaluatedDecision) BypassRequested() bool  { return false }
func (bypassedDecision) BypassRequested() bool      { return true }
func (evaluatedDecision) BypassRequested() bool     { return false }

func (notConfiguredDecision) DecisionSnapshot() *Condition         { return nil }
func (decision notEvaluatedDecision) DecisionSnapshot() *Condition { return &decision.snapshot }
func (decision bypassedDecision) DecisionSnapshot() *Condition     { return &decision.snapshot }
func (decision evaluatedDecision) DecisionSnapshot() *Condition    { return &decision.snapshot }

func (notConfiguredDecision) DecisionEvaluation() *ConditionEvaluation { return nil }
func (notEvaluatedDecision) DecisionEvaluation() *ConditionEvaluation  { return nil }
func (bypassedDecision) DecisionEvaluation() *ConditionEvaluation      { return nil }
func (decision evaluatedDecision) DecisionEvaluation() *ConditionEvaluation {
	return &decision.evaluation
}

// NotConfiguredDecision marks a definition that omitted Conditions. An explicit
// manual bypass of an unconditioned definition records the same decision.
func NotConfiguredDecision() ConditionDecision {
	return notConfiguredDecision{}
}

// NotEvaluatedDecision marks an automatic stale or busy Skip of a configured
// definition: the configured snapshot is retained without evaluation.
func NotEvaluatedDecision(snapshot Condition) ConditionDecision {
	return notEvaluatedDecision{snapshot: snapshot}
}

// BypassedDecision marks an explicit manual bypass of configured Conditions.
func BypassedDecision(snapshot Condition) ConditionDecision {
	return bypassedDecision{snapshot: snapshot}
}

// EvaluatedDecision marks an eligible admission whose Conditions were evaluated.
func EvaluatedDecision(
	snapshot Condition,
	evaluation ConditionEvaluation,
) ConditionDecision {
	return evaluatedDecision{snapshot: snapshot, evaluation: evaluation}
}

// DecideConditions evaluates one configured Condition tree against a covering
// snapshot and reports the evaluated decision plus the Skip reason it implies
// ("" admits).
func DecideConditions(
	conditions *Condition,
	snapshot devices.EntityStateSnapshot,
	at time.Time,
) (ConditionDecision, SkipReason, error) {
	evaluation, err := EvaluateConditions(*conditions, snapshot, at)
	if err != nil {
		return nil, "", err
	}
	decision := EvaluatedDecision(*conditions, evaluation)
	switch evaluation.Result {
	case ConditionTrue:
		return decision, "", nil
	case ConditionFalse:
		return decision, SkipConditionsFalse, nil
	case ConditionUnknown:
		return decision, SkipConditionsUnknown, nil
	default:
		return nil, "", invalid("condition evaluation has an unknown result")
	}
}
