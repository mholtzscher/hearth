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

// Condition is one bounded, identified, discriminated Condition node; exactly
// one family payload is set matching Kind.
type Condition struct {
	ID          ConditionID
	Kind        ConditionKind
	EntityState *EntityStateCondition // entity_state only
	Trigger     *TriggerCondition     // trigger only, invalid at admission
	Children    []Condition           // all/any only, nonempty
	Child       *Condition            // not only
}

// TriggerCondition matches any configured Trigger ID in an immutable Run context.
type TriggerCondition struct{ TriggerIDs []TriggerID }

// TriggerConditionEvidence records the intersection in configured ID order.
type TriggerConditionEvidence struct{ MatchedTriggerIDs []TriggerID }

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

// ConditionNodeResult records one evaluated State or Trigger leaf. SelectedValue is
// nil when no value was selected and the JSON bytes "null" when a JSON null was
// selected, so missing and selected-null stay distinct.
type ConditionNodeResult struct {
	ID            ConditionID
	Result        ConditionResult
	Trigger       *TriggerConditionEvidence // trigger leaf only
	UnknownReason *ConditionUnknownReason   // leaf only, iff Result is unknown
	SelectedValue json.RawMessage           // nil = not selected; "null" = selected null
	ObservationID *devices.ObservationID    // State evidence identity
	ObservedAt    *time.Time                // State evidence time
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
// [EvaluatedDecision], so the envelope invariants (which members accompany
// which mode) hold by construction and need no runtime validation. The
// persisted wire shape is unchanged: mode plus optional snapshot and
// evaluation, with bypass_requested derived from the mode.
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

// conditionDecision is the single unexported implementation; the constructors
// below are the only way to build one.
type conditionDecision struct {
	mode       ConditionDecisionMode
	snapshot   *Condition
	evaluation *ConditionEvaluation
}

func (decision conditionDecision) DecisionMode() ConditionDecisionMode { return decision.mode }

func (decision conditionDecision) BypassRequested() bool {
	return decision.mode == ConditionDecisionBypassed
}

func (decision conditionDecision) DecisionSnapshot() *Condition { return decision.snapshot }

func (decision conditionDecision) DecisionEvaluation() *ConditionEvaluation {
	return decision.evaluation
}

func (conditionDecision) isConditionDecision() {}

// NotConfiguredDecision marks a definition that omitted Conditions. An explicit
// manual bypass of an unconditioned definition records the same decision.
func NotConfiguredDecision() ConditionDecision {
	return conditionDecision{mode: ConditionDecisionNotConfigured}
}

// NotEvaluatedDecision marks an automatic stale or busy Skip of a configured
// definition: the configured snapshot is retained without evaluation.
func NotEvaluatedDecision(snapshot Condition) ConditionDecision {
	return conditionDecision{mode: ConditionDecisionNotEvaluated, snapshot: &snapshot}
}

// BypassedDecision marks an explicit manual bypass of configured Conditions.
func BypassedDecision(snapshot Condition) ConditionDecision {
	return conditionDecision{mode: ConditionDecisionBypassed, snapshot: &snapshot}
}

// EvaluatedDecision marks an eligible admission whose Conditions were evaluated.
func EvaluatedDecision(
	snapshot Condition,
	evaluation ConditionEvaluation,
) ConditionDecision {
	return conditionDecision{
		mode:       ConditionDecisionEvaluated,
		snapshot:   &snapshot,
		evaluation: &evaluation,
	}
}
