package automations

import (
	"encoding/json"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Condition tree bounds from the settled product contract. The 64 KiB
// normalized definition limit is enforced by the definition codec, not here.
const (
	// automationConditionMaxNodes bounds one admission or branch Condition root.
	automationConditionMaxNodes = 64
	// automationConditionMaxDepth bounds Condition nesting, counting the root as
	// depth 1.
	automationConditionMaxDepth = 8
	// automationConditionMaxAgeSeconds bounds one leaf's optional evidence age.
	automationConditionMaxAgeSeconds int64 = 2_592_000
)

// ConditionID identifies one node within its root tree. Admission and each branch
// root have independent namespaces, separate from Trigger and Step IDs.
type ConditionID string

// ConditionKind is the closed discriminated family of one Condition node.
type ConditionKind string

const (
	// ConditionEntityState compares one selected current-State value.
	ConditionEntityState ConditionKind = "entity_state"
	// ConditionTrigger matches recorded Trigger IDs, only in branch Conditions.
	ConditionTrigger ConditionKind = "trigger"
	// ConditionAll requires every child to be true.
	ConditionAll ConditionKind = "all"
	// ConditionAny requires at least one child to be true.
	ConditionAny ConditionKind = "any"
	// ConditionNot negates one child, preserving unknown.
	ConditionNot ConditionKind = "not"
)

// EntityStateCondition compares one retained State selection with a static
// operand. Pointer is an RFC 6901 JSON Pointer relative to State.Value; the
// empty pointer selects the whole value, and MaxAgeSeconds nil means unbounded.
type EntityStateCondition struct {
	EntityID      devices.EntityID
	Pointer       string
	Operator      ComparisonOperator
	Operand       json.RawMessage
	MaxAgeSeconds *int64
}

// Condition is one identified concrete Condition value.
type Condition struct {
	ID   ConditionID
	Body ConditionBody
}

// ConditionBody describes one supported Condition value.
//
//sumtype:decl
type ConditionBody interface{ isConditionBody() }

// AllCondition requires every child to match.
type AllCondition struct{ Children []Condition }

// AnyCondition requires at least one child to match.
type AnyCondition struct{ Children []Condition }

// NotCondition negates its child.
type NotCondition struct{ Child Condition }

func (EntityStateCondition) isConditionBody() {}
func (TriggerCondition) isConditionBody()     {}
func (AllCondition) isConditionBody()         {}
func (AnyCondition) isConditionBody()         {}
func (NotCondition) isConditionBody()         {}

// TriggerCondition matches any configured Trigger ID in an immutable Run context.
type TriggerCondition struct{ TriggerIDs []TriggerID }

// ConditionResult is the three-valued result of one Condition node or tree.
type ConditionResult string

const (
	// ConditionTrue admits an admission decision.
	ConditionTrue ConditionResult = "true"
	// ConditionFalse blocks an admission decision.
	ConditionFalse ConditionResult = "false"
	// ConditionUnknown blocks admission because evidence is unusable.
	ConditionUnknown ConditionResult = "unknown"
)

// ConditionUnknownReason explains why one leaf's evidence is unusable.
// The listed order is the reason precedence when several apply.
type ConditionUnknownReason string

const (
	// ConditionUnknownEntityMissing marks an explicitly requested
	// Entity that does not exist at the snapshot read.
	ConditionUnknownEntityMissing ConditionUnknownReason = "entity_missing"
	// ConditionUnknownStateMissing marks an Entity that exists but has
	// no accepted State.
	ConditionUnknownStateMissing ConditionUnknownReason = "state_missing"
	// ConditionUnknownEvidenceInFuture marks bounded evidence observed
	// after the evaluation time.
	ConditionUnknownEvidenceInFuture ConditionUnknownReason = "evidence_in_future"
	// ConditionUnknownEvidenceExpired marks bounded evidence older than
	// its configured maximum age.
	ConditionUnknownEvidenceExpired ConditionUnknownReason = "evidence_expired"
	// ConditionUnknownPointerMissing marks a valid pointer that cannot
	// select a value, including a noncanonical runtime array index.
	ConditionUnknownPointerMissing ConditionUnknownReason = "pointer_missing"
	// ConditionUnknownTypeMismatch marks an operand and selection that
	// cannot be compared under the operator.
	ConditionUnknownTypeMismatch ConditionUnknownReason = "type_mismatch"
)

// ConditionNodeResult identifies one evaluated State or Trigger leaf and its
// concrete evidence. Its three-valued result is derived by Result.
type ConditionNodeResult struct {
	ID       ConditionID
	Evidence ConditionEvidence
}

// ConditionEvaluation contains every evaluated leaf result in
// definition pre-order, so history explains the decision from its leaves.
type ConditionEvaluation struct {
	EvaluatedAt time.Time
	Result      ConditionResult
	Nodes       []ConditionNodeResult
}

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
