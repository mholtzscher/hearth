package automations

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// EvaluateConditions validates and evaluates an admission-only tree against a coherent State
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
	required, err := RequiredConditionEntityIDs(root)
	if err != nil {
		return ConditionEvaluation{}, err
	}
	for _, id := range required {
		if _, covered := snapshot.Entries[id]; !covered {
			return ConditionEvaluation{}, &ConditionSnapshotRequiredError{RequiredEntityIDs: required}
		}
	}
	return evaluateCoveredConditions(root, snapshot, evaluatedAt, nil)
}

// evaluateCoveredConditions consumes a structurally validated root, a snapshot
// whose coverage the caller has checked, and an immutable Run match set. Admission
// checks one root; branching checks all immediate roots before evaluating any.
// Branch callers retain completed alternatives on operational errors.
func evaluateCoveredConditions(
	root Condition,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
	matchedTriggerIDs []TriggerID,
) (ConditionEvaluation, error) {
	decisionAt := evaluatedAt.UTC()
	nodes := make([]ConditionNodeResult, 0)
	result, err := evaluateConditionNode(&root, snapshot, decisionAt, &nodes, matchedTriggerIDs)
	if err != nil {
		return ConditionEvaluation{}, err
	}
	return ConditionEvaluation{EvaluatedAt: decisionAt, Result: result, Nodes: nodes}, nil
}

// evaluateConditionNode evaluates one node, appending a result for every
// State or Trigger leaf in pre-order, and returns the node's three-valued result.
// Group and not results are derivable from their children, so only leaves are
// recorded as evidence.
func evaluateConditionNode(
	node *Condition,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
	nodes *[]ConditionNodeResult,
	matchedTriggerIDs []TriggerID,
) (ConditionResult, error) {
	switch body := node.Body.(type) {
	case TriggerCondition:
		matches := make([]TriggerID, 0)
		for _, id := range body.TriggerIDs {
			if slices.Contains(matchedTriggerIDs, id) {
				matches = append(matches, id)
			}
		}
		result := conditionResultFromMatch(len(matches) > 0)
		*nodes = append(*nodes, ConditionNodeResult{ID: node.ID,
			Evidence: TriggerMatchEvidence{MatchedTriggerIDs: matches}})
		return result, nil
	case EntityStateCondition:
		result, err := evaluateEntityStateLeaf(node.ID, body, snapshot, evaluatedAt)
		if err != nil {
			return "", err
		}
		*nodes = append(*nodes, result)
		return result.Result(), nil
	case AllCondition:
		return evaluateConditionChildren(body.Children, ConditionAll, snapshot, evaluatedAt, nodes, matchedTriggerIDs)
	case AnyCondition:
		return evaluateConditionChildren(body.Children, ConditionAny, snapshot, evaluatedAt, nodes, matchedTriggerIDs)
	case NotCondition:
		child, err := evaluateConditionNode(&body.Child, snapshot, evaluatedAt, nodes, matchedTriggerIDs)
		if err != nil {
			return "", err
		}
		return negateConditionResult(child), nil
	default:
		return "", invalid("condition %q: unknown kind %q", node.ID, node.Kind())
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
	evidence := UnknownStateEvidence{}
	if !entry.Exists {
		return unknownConditionNode(id, evidence, ConditionUnknownEntityMissing), nil
	}
	if entry.State == nil {
		return unknownConditionNode(id, evidence, ConditionUnknownStateMissing), nil
	}
	observationID := entry.State.ObservationID
	observedAt := entry.State.ObservedAt.UTC()
	evidence.Observation = &ObservationEvidence{ObservationID: observationID, ObservedAt: observedAt}

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
		evidence.SelectedValue = encoded
	}
	if reason, bounded := boundedEvidenceUnknownReason(condition, observedAt, evaluatedAt); bounded {
		return unknownConditionNode(id, evidence, reason), nil
	}
	if !found {
		return unknownConditionNode(id, evidence, ConditionUnknownPointerMissing), nil
	}
	operand, err := decodeJSONValue(condition.Operand)
	if err != nil {
		return ConditionNodeResult{}, fmt.Errorf(
			"%w: condition operand is not exactly one JSON value", ErrInvalidAutomation,
		)
	}
	if !conditionComparisonCompatible(condition.Operator, selected, operand) {
		return unknownConditionNode(id, evidence, ConditionUnknownTypeMismatch), nil
	}
	matched, err := compareJSONValues(condition.Operator, selected, operand)
	if err != nil {
		return ConditionNodeResult{}, err
	}
	return ConditionNodeResult{ID: id, Evidence: KnownStateEvidence{
		Matched: matched, Observation: *evidence.Observation, SelectedValue: evidence.SelectedValue,
	}}, nil
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
	id ConditionID,
	evidence UnknownStateEvidence,
	reason ConditionUnknownReason,
) ConditionNodeResult {
	evidence.Reason = reason
	return ConditionNodeResult{ID: id, Evidence: evidence}
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

func evaluateConditionChildren(
	children []Condition,
	kind ConditionKind,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
	nodes *[]ConditionNodeResult,
	matched []TriggerID,
) (ConditionResult, error) {
	results := make([]ConditionResult, 0, len(children))
	for index := range children {
		result, err := evaluateConditionNode(&children[index], snapshot, evaluatedAt, nodes, matched)
		if err != nil {
			return "", err
		}
		results = append(results, result)
	}
	return combineConditionGroup(kind, results), nil
}
