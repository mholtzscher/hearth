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
	walk := newConditionTreeWalk()
	if err := walk.visit(&root, 1); err != nil {
		return ConditionEvaluation{}, err
	}
	return evaluatePreparedConditions(root, snapshot, evaluatedAt, nil)
}

// evaluatePreparedConditions consumes an unchanged normalized admission or branch
// root and an immutable Run match set. It shares State and boolean evaluation;
// callers at branch boundaries retain completed alternatives on operational errors.
func evaluatePreparedConditions(
	root Condition,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
	matchedTriggerIDs []TriggerID,
) (ConditionEvaluation, error) {
	required := requiredValidatedConditionEntityIDs(root)
	for _, id := range required {
		if _, covered := snapshot.Entries[id]; !covered {
			return ConditionEvaluation{}, &ConditionSnapshotRequiredError{RequiredEntityIDs: required}
		}
	}
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
	switch node.Kind {
	case ConditionTrigger:
		matches := make([]TriggerID, 0)
		for _, id := range node.Trigger.TriggerIDs {
			if slices.Contains(matchedTriggerIDs, id) {
				matches = append(matches, id)
			}
		}
		result := conditionResultFromMatch(len(matches) > 0)
		*nodes = append(*nodes, ConditionNodeResult{ID: node.ID, Result: result,
			Trigger: &TriggerConditionEvidence{MatchedTriggerIDs: matches}})
		return result, nil
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
			child, err := evaluateConditionNode(&node.Children[index], snapshot, evaluatedAt, nodes, matchedTriggerIDs)
			if err != nil {
				return "", err
			}
			childResults = append(childResults, child)
		}
		return combineConditionGroup(node.Kind, childResults), nil
	case ConditionNot:
		child, err := evaluateConditionNode(node.Child, snapshot, evaluatedAt, nodes, matchedTriggerIDs)
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
