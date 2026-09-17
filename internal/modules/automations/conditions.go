package automations

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Condition tree bounds from the settled product contract. The 64 KiB
// normalized definition limit is enforced by the definition codec, not here.
const (
	// automationConditionMaxNodes bounds one definition's whole Condition tree.
	automationConditionMaxNodes = 64
	// automationConditionMaxDepth bounds Condition nesting, counting the root as
	// depth 1.
	automationConditionMaxDepth = 8
	// automationConditionMaxAgeSeconds bounds one leaf's optional evidence age.
	automationConditionMaxAgeSeconds int64 = 2_592_000
)

// ConditionID identifies one node within a definition's Condition tree, unique
// across the tree; the Trigger, Step, and Condition ID namespaces are independent.
type ConditionID string

// ConditionKind is the closed discriminated family of one Condition node.
type ConditionKind string

const (
	// ConditionEntityState compares one selected current-State value.
	ConditionEntityState ConditionKind = "entity_state"
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
	Children    []Condition           // all/any only, nonempty
	Child       *Condition            // not only
}

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

// ConditionNodeResult records one evaluated entity_state leaf. SelectedValue is
// nil when no value was selected and the JSON bytes "null" when a JSON null was
// selected, so missing and selected-null stay distinct.
type ConditionNodeResult struct {
	ID            ConditionID
	Result        ConditionResult
	UnknownReason *ConditionUnknownReason // leaf only, iff Result is unknown
	SelectedValue json.RawMessage         // nil = not selected; "null" = selected null
	ObservationID *devices.ObservationID  // State evidence identity
	ObservedAt    *time.Time              // State evidence time
}

// ConditionEvaluation contains every evaluated entity_state leaf result in
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

// NormalizeConditions validates one typed Condition tree and returns an owned copy of it.
func NormalizeConditions(root Condition) (Condition, error) {
	if err := validateAutomationConditionTree(root); err != nil {
		return Condition{}, err
	}
	return cloneAutomationCondition(root), nil
}

// RequiredConditionEntityIDs returns the sorted, deduplicated Entity IDs every
// entity_state leaf explicitly requests.
func RequiredConditionEntityIDs(root Condition) ([]devices.EntityID, error) {
	walk := newConditionTreeWalk()
	if err := walk.visit(&root, 1); err != nil {
		return nil, err
	}
	if len(walk.entityIDs) == 0 {
		return nil, nil
	}
	slices.Sort(walk.entityIDs)
	return slices.Compact(walk.entityIDs), nil
}

// EvaluateConditions evaluates every node of one tree against a coherent State
// snapshot in definition pre-order, without short-circuiting. A missing snapshot
// key returns a typed [ConditionSnapshotRequiredError] carrying the whole tree's
// required Entity set, never a leaf result. Only a covered Exists=false entry
// produces entity_missing; only a covered Exists=true, State=nil entry produces
// state_missing.
func EvaluateConditions(
	root Condition,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
) (ConditionEvaluation, error) {
	walk := newConditionTreeWalk()
	if err := walk.visit(&root, 1); err != nil {
		return ConditionEvaluation{}, err
	}
	required := slices.Clone(walk.entityIDs)
	slices.Sort(required)
	required = slices.Compact(required)
	for _, entityID := range required {
		if _, covered := snapshot.Entries[entityID]; !covered {
			return ConditionEvaluation{}, &ConditionSnapshotRequiredError{RequiredEntityIDs: required}
		}
	}
	decisionAt := evaluatedAt.UTC()
	nodes := make([]ConditionNodeResult, 0, walk.nodes)
	result, err := evaluateConditionNode(&root, snapshot, decisionAt, &nodes)
	if err != nil {
		return ConditionEvaluation{}, err
	}
	return ConditionEvaluation{EvaluatedAt: decisionAt, Result: result, Nodes: nodes}, nil
}

// conditionTreeWalk validates one typed tree while collecting node IDs, entity
// IDs, and the node count. Depth and node-count bounds keep recursion safe;
// trees originate from JSON decoding, so cycles and shared payloads cannot occur.
type conditionTreeWalk struct {
	ids       map[ConditionID]struct{}
	entityIDs []devices.EntityID
	nodes     int
}

func newConditionTreeWalk() *conditionTreeWalk {
	return &conditionTreeWalk{
		ids: map[ConditionID]struct{}{},
	}
}

// validateAutomationConditionTree validates one typed tree without producing an owned copy.
func validateAutomationConditionTree(root Condition) error {
	return newConditionTreeWalk().visit(&root, 1)
}

// visit validates one node and its descendants. depth is 1 for the root.
func (walk *conditionTreeWalk) visit(node *Condition, depth int) error {
	if depth > automationConditionMaxDepth {
		return invalid("condition %q: tree exceeds the maximum depth", node.ID)
	}
	walk.nodes++
	if walk.nodes > automationConditionMaxNodes {
		return invalid("condition %q: tree exceeds the maximum node count", node.ID)
	}
	if !subjectSlugPattern.MatchString(string(node.ID)) {
		return fmt.Errorf("%w: condition ID %q is not a subject-safe slug", ErrInvalidAutomation, node.ID)
	}
	if _, duplicate := walk.ids[node.ID]; duplicate {
		return invalid("condition %q: condition IDs must be unique within a tree", node.ID)
	}
	walk.ids[node.ID] = struct{}{}
	switch node.Kind {
	case ConditionEntityState:
		return walk.visitEntityState(node)
	case ConditionAll, ConditionAny:
		return walk.visitGroup(node, depth)
	case ConditionNot:
		return walk.visitNot(node, depth)
	default:
		return invalid("condition %q: unknown kind %q", node.ID, node.Kind)
	}
}

func (walk *conditionTreeWalk) visitEntityState(node *Condition) error {
	if node.EntityState == nil || node.Children != nil || node.Child != nil {
		return invalid("condition %q: entity_state family payload mismatch", node.ID)
	}
	if err := validateEntityStateConditionValue(*node.EntityState); err != nil {
		return err
	}
	walk.entityIDs = append(walk.entityIDs, node.EntityState.EntityID)
	return nil
}

func (walk *conditionTreeWalk) visitGroup(node *Condition, depth int) error {
	if node.EntityState != nil || node.Child != nil {
		return invalid("condition %q: all/any family payload mismatch", node.ID)
	}
	if len(node.Children) == 0 {
		return invalid("condition %q: all/any requires a nonempty children array", node.ID)
	}
	for index := range node.Children {
		if err := walk.visit(&node.Children[index], depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (walk *conditionTreeWalk) visitNot(node *Condition, depth int) error {
	if node.EntityState != nil || node.Children != nil {
		return invalid("condition %q: not family payload mismatch", node.ID)
	}
	if node.Child == nil {
		return invalid("condition %q: not requires exactly one child", node.ID)
	}
	return walk.visit(node.Child, depth+1)
}

// validateEntityStateConditionValue checks one leaf's Entity, pointer, operator,
// operand, and evidence age bound.
func validateEntityStateConditionValue(condition EntityStateCondition) error {
	if _, err := devices.ParseEntityID(string(condition.EntityID)); err != nil {
		return fmt.Errorf("%w: condition entity: %w", ErrInvalidAutomation, err)
	}
	if err := validateConditionComparison(condition.Pointer, condition.Operator, condition.Operand); err != nil {
		return err
	}
	if condition.MaxAgeSeconds != nil {
		age := *condition.MaxAgeSeconds
		if age < 1 || age > automationConditionMaxAgeSeconds {
			return fmt.Errorf(
				"%w: max_age_seconds must be between 1 and %d",
				ErrInvalidAutomation, automationConditionMaxAgeSeconds,
			)
		}
	}
	return nil
}

// cloneAutomationCondition returns an owned deep copy so a caller cannot mutate
// a normalized tree through shared slices, pointers, or operand bytes.
func cloneAutomationCondition(condition Condition) Condition {
	cloned := Condition{ID: condition.ID, Kind: condition.Kind}
	switch condition.Kind {
	case ConditionEntityState:
		if condition.EntityState != nil {
			cloned.EntityState = cloneEntityStateCondition(*condition.EntityState)
		}
	case ConditionAll, ConditionAny:
		if len(condition.Children) > 0 {
			cloned.Children = make([]Condition, 0, len(condition.Children))
			for _, child := range condition.Children {
				cloned.Children = append(cloned.Children, cloneAutomationCondition(child))
			}
		}
	case ConditionNot:
		if condition.Child != nil {
			child := cloneAutomationCondition(*condition.Child)
			cloned.Child = &child
		}
	default:
	}
	return cloned
}

func cloneEntityStateCondition(condition EntityStateCondition) *EntityStateCondition {
	cloned := condition
	cloned.Operand = append(json.RawMessage(nil), condition.Operand...)
	if condition.MaxAgeSeconds != nil {
		age := *condition.MaxAgeSeconds
		cloned.MaxAgeSeconds = &age
	}
	return &cloned
}

// evaluateConditionNode evaluates one node, appending a result for every
// entity_state leaf in pre-order, and returns the node's three-valued result.
// Group and not results are derivable from their children, so only leaves are
// recorded as evidence.
func evaluateConditionNode(
	node *Condition,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
	nodes *[]ConditionNodeResult,
) (ConditionResult, error) {
	switch node.Kind {
	case ConditionEntityState:
		result, err := evaluateEntityStateLeaf(node.ID, *node.EntityState, snapshot, evaluatedAt)
		if err != nil {
			return "", err
		}
		*nodes = append(*nodes, result)
		return result.Result, nil
	case ConditionAll, ConditionAny:
		childResults := make([]ConditionResult, 0, len(node.Children))
		for index := range node.Children {
			child, err := evaluateConditionNode(&node.Children[index], snapshot, evaluatedAt, nodes)
			if err != nil {
				return "", err
			}
			childResults = append(childResults, child)
		}
		return combineConditionGroup(node.Kind, childResults), nil
	case ConditionNot:
		child, err := evaluateConditionNode(node.Child, snapshot, evaluatedAt, nodes)
		if err != nil {
			return "", err
		}
		return negateConditionResult(child), nil
	default:
		return "", invalid("condition %q: unknown kind %q", node.ID, node.Kind)
	}
}

// evaluateEntityStateLeaf applies the unknown-reason precedence: missing Entity,
// missing State, future bounded evidence, expired bounded evidence, unresolved
// pointer, then incompatible types. A valid same-type comparison produces
// true or false.
func evaluateEntityStateLeaf(
	id ConditionID,
	condition EntityStateCondition,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
) (ConditionNodeResult, error) {
	entry, covered := snapshot.Entries[condition.EntityID]
	if !covered {
		// Coverage is verified before evaluation begins; this keeps the leaf
		// contract total even if a caller reaches it directly.
		return ConditionNodeResult{}, &ConditionSnapshotRequiredError{
			RequiredEntityIDs: []devices.EntityID{condition.EntityID},
		}
	}
	result := ConditionNodeResult{ID: id}
	if !entry.Exists {
		return unknownConditionNode(result, ConditionUnknownEntityMissing), nil
	}
	if entry.State == nil {
		return unknownConditionNode(result, ConditionUnknownStateMissing), nil
	}
	observationID := entry.State.ObservationID
	observedAt := entry.State.ObservedAt.UTC()
	result.ObservationID = &observationID
	result.ObservedAt = &observedAt

	document, err := decodeJSONValue(json.RawMessage(entry.State.Value))
	if err != nil {
		return ConditionNodeResult{}, fmt.Errorf(
			"%w: stored State for entity %q is corrupt",
			devices.ErrEntityStateSnapshotCorrupt, condition.EntityID,
		)
	}
	tokens, err := parseJSONPointer(condition.Pointer)
	if err != nil {
		return ConditionNodeResult{}, err
	}
	selected, found := lookupJSONPointer(document, tokens)
	if found {
		encoded, encodeErr := json.Marshal(selected)
		if encodeErr != nil {
			return ConditionNodeResult{}, fmt.Errorf(
				"%w: selected State value cannot be encoded", ErrInvalidAutomation,
			)
		}
		result.SelectedValue = encoded
	}
	if reason, bounded := boundedEvidenceUnknownReason(condition, observedAt, evaluatedAt); bounded {
		return unknownConditionNode(result, reason), nil
	}
	if !found {
		return unknownConditionNode(result, ConditionUnknownPointerMissing), nil
	}
	operand, err := decodeJSONValue(condition.Operand)
	if err != nil {
		return ConditionNodeResult{}, fmt.Errorf(
			"%w: condition operand is not exactly one JSON value", ErrInvalidAutomation,
		)
	}
	if !conditionComparisonCompatible(condition.Operator, selected, operand) {
		return unknownConditionNode(result, ConditionUnknownTypeMismatch), nil
	}
	matched, err := compareJSONValues(condition.Operator, selected, operand)
	if err != nil {
		return ConditionNodeResult{}, err
	}
	result.Result = conditionResultFromMatch(matched)
	return result, nil
}

// boundedEvidenceUnknownReason returns the future or expired reason when an
// evidence age bound applies and is violated. Future evidence is unknown only
// with a bound; without one, retained State is compared regardless of age.
func boundedEvidenceUnknownReason(
	condition EntityStateCondition,
	observedAt time.Time,
	evaluatedAt time.Time,
) (ConditionUnknownReason, bool) {
	if condition.MaxAgeSeconds == nil {
		return "", false
	}
	age := evaluatedAt.Sub(observedAt)
	if age < 0 {
		return ConditionUnknownEvidenceInFuture, true
	}
	if age > time.Duration(*condition.MaxAgeSeconds)*time.Second {
		return ConditionUnknownEvidenceExpired, true
	}
	return "", false
}

func unknownConditionNode(
	result ConditionNodeResult,
	reason ConditionUnknownReason,
) ConditionNodeResult {
	result.Result = ConditionUnknown
	result.UnknownReason = &reason
	return result
}

func conditionResultFromMatch(matched bool) ConditionResult {
	if matched {
		return ConditionTrue
	}
	return ConditionFalse
}

// conditionComparisonCompatible reports whether two selected values can be
// compared under the operator.
func conditionComparisonCompatible(operator ComparisonOperator, left, right any) bool {
	if operator.isOrdering() {
		_, leftNumeric := jsonNumberValue(left)
		_, rightNumeric := jsonNumberValue(right)
		return leftNumeric && rightNumeric
	}
	return jsonValueKind(left) == jsonValueKind(right)
}

// combineConditionGroup applies the truth table for a nonempty child group.
func combineConditionGroup(
	kind ConditionKind,
	results []ConditionResult,
) ConditionResult {
	if kind == ConditionAny {
		return combineAnyConditionResults(results)
	}
	return combineAllConditionResults(results)
}

func combineAllConditionResults(results []ConditionResult) ConditionResult {
	unknown := false
	for _, result := range results {
		switch result {
		case ConditionFalse:
			return ConditionFalse
		case ConditionUnknown:
			unknown = true
		case ConditionTrue:
		default:
		}
	}
	if unknown {
		return ConditionUnknown
	}
	return ConditionTrue
}

func combineAnyConditionResults(results []ConditionResult) ConditionResult {
	unknown := false
	for _, result := range results {
		switch result {
		case ConditionTrue:
			return ConditionTrue
		case ConditionUnknown:
			unknown = true
		case ConditionFalse:
		default:
		}
	}
	if unknown {
		return ConditionUnknown
	}
	return ConditionFalse
}

// negateConditionResult inverts true and false while preserving unknown.
func negateConditionResult(result ConditionResult) ConditionResult {
	switch result {
	case ConditionTrue:
		return ConditionFalse
	case ConditionFalse:
		return ConditionTrue
	case ConditionUnknown:
		return ConditionUnknown
	default:
		return result
	}
}
