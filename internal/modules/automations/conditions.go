package automations

import (
	"encoding/json"
	"fmt"
	"reflect"
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

// AutomationConditionID identifies one node within a definition's Condition
// tree. It uses the shared subject-safe slug grammar and is unique across the
// whole tree; the Trigger, Step, and Condition ID namespaces are independent.
type AutomationConditionID string

// AutomationConditionKind is the closed discriminated family of one Condition
// node. Kind determines which payload field is meaningful.
type AutomationConditionKind string

const (
	// AutomationConditionEntityState compares one selected current-State value.
	AutomationConditionEntityState AutomationConditionKind = "entity_state"
	// AutomationConditionAll requires every child to be true.
	AutomationConditionAll AutomationConditionKind = "all"
	// AutomationConditionAny requires at least one child to be true.
	AutomationConditionAny AutomationConditionKind = "any"
	// AutomationConditionNot negates one child, preserving unknown.
	AutomationConditionNot AutomationConditionKind = "not"
)

// EntityStateCondition compares one retained State selection with a static
// operand. Operand is exactly one normalized JSON value. Pointer is an RFC 6901
// JSON Pointer relative to State.Value; the empty pointer selects the whole
// value. MaxAgeSeconds is nil when no evidence age bound applies.
type EntityStateCondition struct {
	EntityID      devices.EntityID
	Pointer       string
	Operator      ComparisonOperator
	Operand       json.RawMessage
	MaxAgeSeconds *int64
}

// AutomationCondition is one bounded, identified, discriminated Condition node.
// Exactly one family payload is set: EntityState iff Kind is
// AutomationConditionEntityState, Children iff Kind is AutomationConditionAll or
// AutomationConditionAny, and Child iff Kind is AutomationConditionNot.
type AutomationCondition struct {
	ID          AutomationConditionID
	Kind        AutomationConditionKind
	EntityState *EntityStateCondition // entity_state only
	Children    []AutomationCondition // all/any only, nonempty
	Child       *AutomationCondition  // not only
}

// AutomationConditionResult is the three-valued result of one Condition node or
// tree. Only a true root admits a Run.
type AutomationConditionResult string

const (
	// AutomationConditionTrue admits an admission decision.
	AutomationConditionTrue AutomationConditionResult = "true"
	// AutomationConditionFalse blocks an admission decision.
	AutomationConditionFalse AutomationConditionResult = "false"
	// AutomationConditionUnknown blocks admission because evidence is unusable.
	AutomationConditionUnknown AutomationConditionResult = "unknown"
)

// isKnown reports whether result is one of the three closed result values.
func (result AutomationConditionResult) isKnown() bool {
	switch result {
	case AutomationConditionTrue, AutomationConditionFalse, AutomationConditionUnknown:
		return true
	default:
		return false
	}
}

// AutomationConditionUnknownReason explains why one leaf's evidence is unusable.
// The listed order is the reason precedence when several apply.
type AutomationConditionUnknownReason string

const (
	// AutomationConditionUnknownEntityMissing marks an explicitly requested
	// Entity that does not exist at the snapshot read.
	AutomationConditionUnknownEntityMissing AutomationConditionUnknownReason = "entity_missing"
	// AutomationConditionUnknownStateMissing marks an Entity that exists but has
	// no accepted State.
	AutomationConditionUnknownStateMissing AutomationConditionUnknownReason = "state_missing"
	// AutomationConditionUnknownEvidenceInFuture marks bounded evidence observed
	// after the evaluation time.
	AutomationConditionUnknownEvidenceInFuture AutomationConditionUnknownReason = "evidence_in_future"
	// AutomationConditionUnknownEvidenceExpired marks bounded evidence older than
	// its configured maximum age.
	AutomationConditionUnknownEvidenceExpired AutomationConditionUnknownReason = "evidence_expired"
	// AutomationConditionUnknownPointerMissing marks a valid pointer that cannot
	// select a value, including a noncanonical runtime array index.
	AutomationConditionUnknownPointerMissing AutomationConditionUnknownReason = "pointer_missing"
	// AutomationConditionUnknownTypeMismatch marks an operand and selection that
	// cannot be compared under the operator.
	AutomationConditionUnknownTypeMismatch AutomationConditionUnknownReason = "type_mismatch"
)

// isKnown reports whether reason is one of the six closed unknown reasons.
func (reason AutomationConditionUnknownReason) isKnown() bool {
	switch reason {
	case AutomationConditionUnknownEntityMissing,
		AutomationConditionUnknownStateMissing,
		AutomationConditionUnknownEvidenceInFuture,
		AutomationConditionUnknownEvidenceExpired,
		AutomationConditionUnknownPointerMissing,
		AutomationConditionUnknownTypeMismatch:
		return true
	default:
		return false
	}
}

// AutomationConditionNodeResult records one evaluated node. SelectedValue is nil
// when no value was selected and the JSON bytes "null" when a JSON null was
// selected, so missing and selected-null stay distinct. ObservationID and
// ObservedAt are set together whenever State evidence existed.
type AutomationConditionNodeResult struct {
	ID            AutomationConditionID
	Result        AutomationConditionResult
	UnknownReason *AutomationConditionUnknownReason // leaf only, iff Result is unknown
	SelectedValue json.RawMessage                   // nil = not selected; "null" = selected null
	ObservationID *devices.ObservationID            // State evidence identity
	ObservedAt    *time.Time                        // State evidence time
}

// AutomationConditionEvaluation contains every node result in definition
// pre-order, so history explains every predicate rather than whichever branch
// happened to short-circuit.
type AutomationConditionEvaluation struct {
	EvaluatedAt time.Time
	Result      AutomationConditionResult
	Nodes       []AutomationConditionNodeResult
}

// AutomationConditionDecisionMode distinguishes retained evidence from
// deliberate non-evaluation.
type AutomationConditionDecisionMode string

const (
	// AutomationConditionDecisionNotConfigured marks a definition that omitted
	// Conditions. It is also the normalized form of legacy unconditioned history.
	AutomationConditionDecisionNotConfigured AutomationConditionDecisionMode = "not_configured"
	// AutomationConditionDecisionNotEvaluated marks an automatic stale or busy
	// Skip of a configured definition.
	AutomationConditionDecisionNotEvaluated AutomationConditionDecisionMode = "not_evaluated"
	// AutomationConditionDecisionBypassed marks an explicit manual bypass.
	AutomationConditionDecisionBypassed AutomationConditionDecisionMode = "bypassed"
	// AutomationConditionDecisionEvaluated marks an eligible admission whose
	// Conditions were evaluated.
	AutomationConditionDecisionEvaluated AutomationConditionDecisionMode = "evaluated"
)

// isKnown reports whether mode is one of the four closed decision modes.
func (mode AutomationConditionDecisionMode) isKnown() bool {
	switch mode {
	case AutomationConditionDecisionNotConfigured,
		AutomationConditionDecisionNotEvaluated,
		AutomationConditionDecisionBypassed,
		AutomationConditionDecisionEvaluated:
		return true
	default:
		return false
	}
}

// AutomationConditionDecision is the immutable admission explanation retained
// with a Run or Skip. It is evidence, not executable work. Legacy unconditioned
// rows decode to the explicit not_configured mode rather than a zero value.
type AutomationConditionDecision struct {
	Mode            AutomationConditionDecisionMode
	BypassRequested bool
	Snapshot        *AutomationCondition
	Evaluation      *AutomationConditionEvaluation
}

// NormalizeAutomationConditions validates one typed Condition tree and returns
// an owned copy of it. It rejects cycles and slice-aliasing hazards before
// unbounded recursion, plus over-depth trees, too many nodes, duplicate IDs,
// contradictory family payloads, invalid pointers, operators, operands, and
// evidence age bounds.
func NormalizeAutomationConditions(root AutomationCondition) (AutomationCondition, error) {
	if err := validateAutomationConditionTree(root); err != nil {
		return AutomationCondition{}, err
	}
	return cloneAutomationCondition(root), nil
}

// RequiredConditionEntityIDs returns the sorted, deduplicated Entity IDs every
// entity_state leaf of one tree explicitly requests. A malformed typed tree
// returns an error instead of a partial set.
func RequiredConditionEntityIDs(root AutomationCondition) ([]devices.EntityID, error) {
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

// EvaluateAutomationConditions evaluates every node of one tree against a
// coherent State snapshot in definition pre-order, without short-circuiting, at
// the supplied admission-decision time.
//
// It first verifies complete coverage: a missing snapshot key returns a typed
// [ConditionSnapshotRequiredError] carrying the whole tree's required Entity set
// and an empty evaluation, and never a leaf result for the uncovered key. Only a
// covered Exists=false entry produces entity_missing, and only a covered
// Exists=true, State=nil entry produces state_missing.
func EvaluateAutomationConditions(
	root AutomationCondition,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
) (AutomationConditionEvaluation, error) {
	walk := newConditionTreeWalk()
	if err := walk.visit(&root, 1); err != nil {
		return AutomationConditionEvaluation{}, err
	}
	required := slices.Clone(walk.entityIDs)
	slices.Sort(required)
	required = slices.Compact(required)
	for _, entityID := range required {
		if _, covered := snapshot.Entries[entityID]; !covered {
			return AutomationConditionEvaluation{}, &ConditionSnapshotRequiredError{RequiredEntityIDs: required}
		}
	}
	decisionAt := evaluatedAt.UTC()
	nodes := make([]AutomationConditionNodeResult, 0, walk.nodes)
	result, err := evaluateConditionNode(&root, snapshot, decisionAt, &nodes)
	if err != nil {
		return AutomationConditionEvaluation{}, err
	}
	return AutomationConditionEvaluation{EvaluatedAt: decisionAt, Result: result, Nodes: nodes}, nil
}

// conditionTreeWalk validates one typed tree while collecting node IDs, entity
// IDs, and the node count. Slice data pointers and Child pointers are tracked so
// cycles and shared payloads are rejected before recursion can run unbounded.
type conditionTreeWalk struct {
	ids       map[AutomationConditionID]struct{}
	slices    map[uintptr]struct{}
	childPtrs map[*AutomationCondition]struct{}
	entityIDs []devices.EntityID
	nodes     int
}

func newConditionTreeWalk() *conditionTreeWalk {
	return &conditionTreeWalk{
		ids:       map[AutomationConditionID]struct{}{},
		slices:    map[uintptr]struct{}{},
		childPtrs: map[*AutomationCondition]struct{}{},
	}
}

// validateAutomationConditionTree validates one typed tree without producing an
// owned copy, so callers that only need integrity can share it with normalization
// and evaluation.
func validateAutomationConditionTree(root AutomationCondition) error {
	return newConditionTreeWalk().visit(&root, 1)
}

// visit validates one node and its descendants. depth is 1 for the root.
func (walk *conditionTreeWalk) visit(node *AutomationCondition, depth int) error {
	if depth > automationConditionMaxDepth {
		return invalidCondition(node.ID, "tree exceeds the maximum depth")
	}
	walk.nodes++
	if walk.nodes > automationConditionMaxNodes {
		return invalidCondition(node.ID, "tree exceeds the maximum node count")
	}
	if !subjectSlugPattern.MatchString(string(node.ID)) {
		return fmt.Errorf("%w: condition ID %q is not a subject-safe slug", ErrInvalidAutomation, node.ID)
	}
	if _, duplicate := walk.ids[node.ID]; duplicate {
		return invalidCondition(node.ID, "condition IDs must be unique within a tree")
	}
	walk.ids[node.ID] = struct{}{}
	switch node.Kind {
	case AutomationConditionEntityState:
		return walk.visitEntityState(node)
	case AutomationConditionAll, AutomationConditionAny:
		return walk.visitGroup(node, depth)
	case AutomationConditionNot:
		return walk.visitNot(node, depth)
	default:
		return invalidCondition(node.ID, fmt.Sprintf("unknown kind %q", node.Kind))
	}
}

func (walk *conditionTreeWalk) visitEntityState(node *AutomationCondition) error {
	if node.EntityState == nil || node.Children != nil || node.Child != nil {
		return invalidCondition(node.ID, "entity_state family payload mismatch")
	}
	if err := validateEntityStateConditionValue(*node.EntityState); err != nil {
		return err
	}
	walk.entityIDs = append(walk.entityIDs, node.EntityState.EntityID)
	return nil
}

func (walk *conditionTreeWalk) visitGroup(node *AutomationCondition, depth int) error {
	if node.EntityState != nil || node.Child != nil {
		return invalidCondition(node.ID, "all/any family payload mismatch")
	}
	if len(node.Children) == 0 {
		return invalidCondition(node.ID, "all/any requires a nonempty children array")
	}
	pointer := reflect.ValueOf(node.Children).Pointer()
	if _, aliased := walk.slices[pointer]; aliased {
		return invalidCondition(node.ID, "children payload is shared or cyclic")
	}
	walk.slices[pointer] = struct{}{}
	for index := range node.Children {
		if err := walk.visit(&node.Children[index], depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (walk *conditionTreeWalk) visitNot(node *AutomationCondition, depth int) error {
	if node.EntityState != nil || node.Children != nil {
		return invalidCondition(node.ID, "not family payload mismatch")
	}
	if node.Child == nil {
		return invalidCondition(node.ID, "not requires exactly one child")
	}
	if _, cyclic := walk.childPtrs[node.Child]; cyclic {
		return invalidCondition(node.ID, "child pointer creates a cycle or is shared")
	}
	walk.childPtrs[node.Child] = struct{}{}
	return walk.visit(node.Child, depth+1)
}

// validateEntityStateConditionValue checks one leaf's Entity, pointer, operator,
// operand, and optional evidence age bound. Pointer existence stays a runtime
// result; only syntax, size, and operand/operator compatibility are definition
// errors.
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
func cloneAutomationCondition(condition AutomationCondition) AutomationCondition {
	cloned := AutomationCondition{ID: condition.ID, Kind: condition.Kind}
	switch condition.Kind {
	case AutomationConditionEntityState:
		if condition.EntityState != nil {
			cloned.EntityState = cloneEntityStateCondition(*condition.EntityState)
		}
	case AutomationConditionAll, AutomationConditionAny:
		if len(condition.Children) > 0 {
			cloned.Children = make([]AutomationCondition, 0, len(condition.Children))
			for _, child := range condition.Children {
				cloned.Children = append(cloned.Children, cloneAutomationCondition(child))
			}
		}
	case AutomationConditionNot:
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

// evaluateConditionNode evaluates one node and appends its result in pre-order,
// returning the node's three-valued result.
func evaluateConditionNode(
	node *AutomationCondition,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
	nodes *[]AutomationConditionNodeResult,
) (AutomationConditionResult, error) {
	switch node.Kind {
	case AutomationConditionEntityState:
		result, err := evaluateEntityStateLeaf(node.ID, *node.EntityState, snapshot, evaluatedAt)
		if err != nil {
			return "", err
		}
		*nodes = append(*nodes, result)
		return result.Result, nil
	case AutomationConditionAll, AutomationConditionAny:
		return evaluateConditionGroup(node, snapshot, evaluatedAt, nodes)
	case AutomationConditionNot:
		position := len(*nodes)
		*nodes = append(*nodes, AutomationConditionNodeResult{ID: node.ID})
		child, err := evaluateConditionNode(node.Child, snapshot, evaluatedAt, nodes)
		if err != nil {
			return "", err
		}
		result := negateConditionResult(child)
		(*nodes)[position].Result = result
		return result, nil
	default:
		return "", invalidCondition(node.ID, fmt.Sprintf("unknown kind %q", node.Kind))
	}
}

func evaluateConditionGroup(
	node *AutomationCondition,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
	nodes *[]AutomationConditionNodeResult,
) (AutomationConditionResult, error) {
	position := len(*nodes)
	*nodes = append(*nodes, AutomationConditionNodeResult{ID: node.ID})
	childResults := make([]AutomationConditionResult, 0, len(node.Children))
	for index := range node.Children {
		child, err := evaluateConditionNode(&node.Children[index], snapshot, evaluatedAt, nodes)
		if err != nil {
			return "", err
		}
		childResults = append(childResults, child)
	}
	result := combineConditionGroup(node.Kind, childResults)
	(*nodes)[position].Result = result
	return result, nil
}

// evaluateEntityStateLeaf applies the unknown-reason precedence: missing Entity,
// missing State, future bounded evidence, expired bounded evidence, unresolved
// pointer, then incompatible types. A valid same-type comparison produces
// true or false.
func evaluateEntityStateLeaf(
	id AutomationConditionID,
	condition EntityStateCondition,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
) (AutomationConditionNodeResult, error) {
	entry, covered := snapshot.Entries[condition.EntityID]
	if !covered {
		// Coverage is verified before evaluation begins; this keeps the leaf
		// contract total even if a caller reaches it directly.
		return AutomationConditionNodeResult{}, &ConditionSnapshotRequiredError{
			RequiredEntityIDs: []devices.EntityID{condition.EntityID},
		}
	}
	result := AutomationConditionNodeResult{ID: id}
	if !entry.Exists {
		return unknownConditionNode(result, AutomationConditionUnknownEntityMissing), nil
	}
	if entry.State == nil {
		return unknownConditionNode(result, AutomationConditionUnknownStateMissing), nil
	}
	observationID := entry.State.ObservationID
	observedAt := entry.State.ObservedAt.UTC()
	result.ObservationID = &observationID
	result.ObservedAt = &observedAt

	document, err := decodeJSONValue(json.RawMessage(entry.State.Value))
	if err != nil {
		return AutomationConditionNodeResult{}, fmt.Errorf(
			"%w: stored State for entity %q is corrupt",
			devices.ErrEntityStateSnapshotCorrupt, condition.EntityID,
		)
	}
	tokens, err := parseJSONPointer(condition.Pointer)
	if err != nil {
		return AutomationConditionNodeResult{}, err
	}
	selected, found := lookupJSONPointer(document, tokens)
	if found {
		encoded, encodeErr := json.Marshal(selected)
		if encodeErr != nil {
			return AutomationConditionNodeResult{}, fmt.Errorf(
				"%w: selected State value cannot be encoded", ErrInvalidAutomation,
			)
		}
		result.SelectedValue = encoded
	}
	if reason, bounded := boundedEvidenceUnknownReason(condition, observedAt, evaluatedAt); bounded {
		return unknownConditionNode(result, reason), nil
	}
	if !found {
		return unknownConditionNode(result, AutomationConditionUnknownPointerMissing), nil
	}
	operand, err := decodeJSONValue(condition.Operand)
	if err != nil {
		return AutomationConditionNodeResult{}, fmt.Errorf(
			"%w: condition operand is not exactly one JSON value", ErrInvalidAutomation,
		)
	}
	if !conditionComparisonCompatible(condition.Operator, selected, operand) {
		return unknownConditionNode(result, AutomationConditionUnknownTypeMismatch), nil
	}
	matched, err := compareJSONValues(condition.Operator, selected, operand)
	if err != nil {
		return AutomationConditionNodeResult{}, err
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
) (AutomationConditionUnknownReason, bool) {
	if condition.MaxAgeSeconds == nil {
		return "", false
	}
	age := evaluatedAt.Sub(observedAt)
	if age < 0 {
		return AutomationConditionUnknownEvidenceInFuture, true
	}
	if age > time.Duration(*condition.MaxAgeSeconds)*time.Second {
		return AutomationConditionUnknownEvidenceExpired, true
	}
	return "", false
}

func unknownConditionNode(
	result AutomationConditionNodeResult,
	reason AutomationConditionUnknownReason,
) AutomationConditionNodeResult {
	result.Result = AutomationConditionUnknown
	result.UnknownReason = &reason
	return result
}

func conditionResultFromMatch(matched bool) AutomationConditionResult {
	if matched {
		return AutomationConditionTrue
	}
	return AutomationConditionFalse
}

// conditionComparisonCompatible reports whether two selected values can be
// compared at all under the operator. eq and ne require the same top-level JSON
// kind; ordering requires both sides to be JSON numbers.
func conditionComparisonCompatible(operator ComparisonOperator, left, right any) bool {
	if operator.isOrdering() {
		_, leftNumeric := jsonNumberValue(left)
		_, rightNumeric := jsonNumberValue(right)
		return leftNumeric && rightNumeric
	}
	return jsonValueKind(left) == jsonValueKind(right)
}

// combineConditionGroup applies the truth table for a nonempty child group.
// all is false if any child is false, otherwise unknown if any child is unknown,
// otherwise true. any is true if any child is true, otherwise unknown if any
// child is unknown, otherwise false.
func combineConditionGroup(
	kind AutomationConditionKind,
	results []AutomationConditionResult,
) AutomationConditionResult {
	if kind == AutomationConditionAny {
		return combineAnyConditionResults(results)
	}
	return combineAllConditionResults(results)
}

func combineAllConditionResults(results []AutomationConditionResult) AutomationConditionResult {
	unknown := false
	for _, result := range results {
		switch result {
		case AutomationConditionFalse:
			return AutomationConditionFalse
		case AutomationConditionUnknown:
			unknown = true
		case AutomationConditionTrue:
		default:
		}
	}
	if unknown {
		return AutomationConditionUnknown
	}
	return AutomationConditionTrue
}

func combineAnyConditionResults(results []AutomationConditionResult) AutomationConditionResult {
	unknown := false
	for _, result := range results {
		switch result {
		case AutomationConditionTrue:
			return AutomationConditionTrue
		case AutomationConditionUnknown:
			unknown = true
		case AutomationConditionFalse:
		default:
		}
	}
	if unknown {
		return AutomationConditionUnknown
	}
	return AutomationConditionFalse
}

// negateConditionResult inverts true and false while preserving unknown.
func negateConditionResult(result AutomationConditionResult) AutomationConditionResult {
	switch result {
	case AutomationConditionTrue:
		return AutomationConditionFalse
	case AutomationConditionFalse:
		return AutomationConditionTrue
	case AutomationConditionUnknown:
		return AutomationConditionUnknown
	default:
		return result
	}
}

func invalidCondition(id AutomationConditionID, message string) error {
	return fmt.Errorf("%w: condition %q: %s", ErrInvalidAutomation, id, message)
}
