package automations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// automationConditionDecisionJSON is the strict persisted decision shape. Every
// field is snake_case and family-inapplicable fields stay absent. Mode and
// BypassRequested are pointers so a missing or JSON-null scalar is rejected
// instead of silently decoding to its zero value.
type automationConditionDecisionJSON struct {
	Mode            *AutomationConditionDecisionMode `json:"mode"`
	BypassRequested *bool                            `json:"bypass_requested"`
	Snapshot        json.RawMessage                  `json:"snapshot,omitempty"`
	Evaluation      json.RawMessage                  `json:"evaluation,omitempty"`
}

type automationConditionEvaluationJSON struct {
	EvaluatedAt time.Time                     `json:"evaluated_at"`
	Result      AutomationConditionResult     `json:"result"`
	Nodes       []automationConditionNodeJSON `json:"nodes"`
}

// automationConditionNodeJSON keeps selected JSON null distinct from missing:
// SelectedValue is absent for "not selected" and the bytes "null" for a selected
// JSON null. The other optional members are raw so an explicit JSON null is
// distinguishable from an absent member and rejected as a placeholder.
type automationConditionNodeJSON struct {
	ID            AutomationConditionID     `json:"id"`
	Result        AutomationConditionResult `json:"result"`
	UnknownReason json.RawMessage           `json:"unknown_reason,omitempty"`
	SelectedValue json.RawMessage           `json:"selected_value,omitempty"`
	ObservationID json.RawMessage           `json:"observation_id,omitempty"`
	ObservedAt    json.RawMessage           `json:"observed_at,omitempty"`
}

// EncodeAutomationConditionDecision validates one decision and renders it in the
// strict persisted shape. It never writes a partial or inconsistent decision.
func EncodeAutomationConditionDecision(decision AutomationConditionDecision) (json.RawMessage, error) {
	if err := ValidateAutomationConditionDecision(decision); err != nil {
		return nil, err
	}
	mode := decision.Mode
	bypass := decision.BypassRequested
	value := automationConditionDecisionJSON{
		Mode:            &mode,
		BypassRequested: &bypass,
	}
	if decision.Snapshot != nil {
		rawSnapshot, err := json.Marshal(encodeAutomationCondition(*decision.Snapshot))
		if err != nil {
			return nil, fmt.Errorf("%w: condition snapshot cannot be encoded: %w", ErrInvalidAutomation, err)
		}
		value.Snapshot = rawSnapshot
	}
	if decision.Evaluation != nil {
		rawEvaluation, err := encodeAutomationConditionEvaluation(*decision.Evaluation)
		if err != nil {
			return nil, err
		}
		value.Evaluation = rawEvaluation
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: condition decision cannot be encoded: %w", ErrInvalidAutomation, err)
	}
	return raw, nil
}

// DecodeAutomationConditionDecision validates and normalizes one persisted
// decision. An empty payload is the legacy unconditioned history row whose
// decision column is SQL NULL; it is normalized to the explicit not_configured
// mode rather than left as a zero-valued mode. Any other malformed decision is a
// permanent [ErrInvalidAutomation].
func DecodeAutomationConditionDecision(raw json.RawMessage) (AutomationConditionDecision, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return AutomationConditionDecision{Mode: AutomationConditionDecisionNotConfigured}, nil
	}
	var value automationConditionDecisionJSON
	if err := decodeStrictJSONObject(raw, &value); err != nil {
		return AutomationConditionDecision{}, decisionInvalid("condition decision is not a strict object")
	}
	if value.Mode == nil || value.BypassRequested == nil {
		return AutomationConditionDecision{}, decisionInvalid(
			"condition decision requires an explicit mode and bypass_requested",
		)
	}
	if isExplicitJSONNull(value.Snapshot) {
		return AutomationConditionDecision{}, decisionInvalid("condition decision snapshot must not be null")
	}
	if isExplicitJSONNull(value.Evaluation) {
		return AutomationConditionDecision{}, decisionInvalid("condition decision evaluation must not be null")
	}
	decision := AutomationConditionDecision{
		Mode:            *value.Mode,
		BypassRequested: *value.BypassRequested,
	}
	if len(bytes.TrimSpace(value.Snapshot)) > 0 {
		codec, codecErr := automationDefinitionCodec()
		if codecErr != nil {
			return AutomationConditionDecision{}, codecErr
		}
		if validationErr := codec.ValidateCondition(value.Snapshot); validationErr != nil {
			return AutomationConditionDecision{}, decisionInvalid(
				"condition snapshot does not satisfy the strict schema",
			)
		}
		var snapshotJSON automationConditionJSON
		if bindErr := json.Unmarshal(value.Snapshot, &snapshotJSON); bindErr != nil {
			return AutomationConditionDecision{}, decisionInvalid("condition snapshot cannot be bound")
		}
		snapshot := automationConditionFromJSON(snapshotJSON)
		decision.Snapshot = &snapshot
	}
	evaluationJSON, decodeErr := decodeConditionEvaluationJSON(value.Evaluation)
	if decodeErr != nil {
		return AutomationConditionDecision{}, decodeErr
	}
	if evaluationJSON != nil {
		evaluation, evaluationErr := automationConditionEvaluationFromJSON(*evaluationJSON)
		if evaluationErr != nil {
			return AutomationConditionDecision{}, evaluationErr
		}
		decision.Evaluation = &evaluation
	}
	if err := ValidateAutomationConditionDecision(decision); err != nil {
		return AutomationConditionDecision{}, err
	}
	return decision, nil
}

// ValidateAutomationConditionDecision enforces the decision invariants that do
// not depend on the enclosing Run or Skip: closed mode, snapshot/evaluation
// presence, bypass coherence, and full evaluation evidence integrity against the
// retained snapshot.
func ValidateAutomationConditionDecision(decision AutomationConditionDecision) error {
	if !decision.Mode.isKnown() {
		return decisionInvalid(fmt.Sprintf("unknown mode %q", decision.Mode))
	}
	switch decision.Mode {
	case AutomationConditionDecisionNotConfigured:
		if decision.Snapshot != nil || decision.Evaluation != nil {
			return decisionInvalid("not_configured carries a snapshot or evaluation")
		}
	case AutomationConditionDecisionNotEvaluated, AutomationConditionDecisionBypassed:
		if decision.Snapshot == nil || decision.Evaluation != nil {
			return decisionInvalid("mode requires a snapshot and no evaluation")
		}
	case AutomationConditionDecisionEvaluated:
		if decision.Snapshot == nil || decision.Evaluation == nil {
			return decisionInvalid("evaluated requires a snapshot and evaluation")
		}
	default:
		return decisionInvalid(fmt.Sprintf("unknown mode %q", decision.Mode))
	}
	if decision.Snapshot == nil {
		return nil
	}
	if err := validateAutomationConditionTree(*decision.Snapshot); err != nil {
		return err
	}
	if decision.BypassRequested != (decision.Mode == AutomationConditionDecisionBypassed) {
		return decisionInvalid("with configured conditions the bypass request matches the bypassed mode")
	}
	if decision.Evaluation != nil {
		return validateConditionEvaluation(*decision.Snapshot, *decision.Evaluation)
	}
	return nil
}

// ValidateRunConditionDecision checks one Run's decision against its full
// definition snapshot: a configured decision snapshot must equal the definition
// snapshot's Conditions, a Run never records not_evaluated, bypass is manual
// only, and an evaluated Run admitted on a true root.
func ValidateRunConditionDecision(run AutomationRun) error {
	decision := run.ConditionDecision
	if err := ValidateAutomationConditionDecision(decision); err != nil {
		return err
	}
	if decision.BypassRequested && run.Source != RunSourceManual {
		return decisionInvalid("only a manual Run may request a bypass")
	}
	if !conditionsEqual(decision.Snapshot, run.Snapshot.Conditions) {
		return decisionInvalid("a Run decision snapshot must equal the definition snapshot conditions")
	}
	switch decision.Mode {
	case AutomationConditionDecisionNotEvaluated:
		return decisionInvalid("a Run never records a not_evaluated decision")
	case AutomationConditionDecisionBypassed:
		if run.Source != RunSourceManual {
			return decisionInvalid("only a manual Run may bypass conditions")
		}
	case AutomationConditionDecisionEvaluated:
		if decision.Evaluation.Result != AutomationConditionTrue {
			return decisionInvalid("an evaluated Run requires a true root result")
		}
	case AutomationConditionDecisionNotConfigured:
	default:
		return decisionInvalid(fmt.Sprintf("unknown mode %q", decision.Mode))
	}
	return nil
}

// ValidateSkipConditionDecision checks one Skip's decision against its reason. A
// Skip never requests or records a bypass; stale and busy never evaluate
// conditions, and a condition Skip must have evaluated false or unknown to match
// its reason. This is the validator that rejects fabricated provenance.
func ValidateSkipConditionDecision(skip AutomationSkip) error {
	decision := skip.ConditionDecision
	if err := ValidateAutomationConditionDecision(decision); err != nil {
		return err
	}
	if decision.Mode == AutomationConditionDecisionBypassed || decision.BypassRequested {
		return decisionInvalid("a Skip is never a bypass")
	}
	switch skip.Reason {
	case AutomationSkipStaleFact, AutomationSkipBusy:
		if decision.Mode == AutomationConditionDecisionEvaluated {
			return decisionInvalid("a stale or busy Skip does not evaluate conditions")
		}
	case AutomationSkipConditionsFalse, AutomationSkipConditionsUnknown:
		if decision.Mode != AutomationConditionDecisionEvaluated {
			return decisionInvalid("a condition Skip requires an evaluated decision")
		}
		want := AutomationConditionFalse
		if skip.Reason == AutomationSkipConditionsUnknown {
			want = AutomationConditionUnknown
		}
		if decision.Evaluation.Result != want {
			return decisionInvalid("a condition Skip result matches its reason")
		}
	default:
		return decisionInvalid(fmt.Sprintf("unknown skip reason %q", skip.Reason))
	}
	return nil
}

// validateConditionEvaluation verifies that evaluation nodes exactly match the
// snapshot nodes in pre-order, that every composite result agrees with its
// recorded children under the truth table, and that each leaf's evidence fits
// its result. It never consults newer State.
func validateConditionEvaluation(snapshot AutomationCondition, evaluation AutomationConditionEvaluation) error {
	if evaluation.EvaluatedAt.IsZero() {
		return decisionInvalid("evaluation requires an evaluated_at time")
	}
	if !evaluation.Result.isKnown() {
		return decisionInvalid("evaluation result must be true, false, or unknown")
	}
	if len(evaluation.Nodes) == 0 {
		return decisionInvalid("evaluation requires at least the root node")
	}
	cursor := 0
	result, err := validateConditionEvaluationNode(&snapshot, evaluation.EvaluatedAt, evaluation.Nodes, &cursor)
	if err != nil {
		return err
	}
	if cursor != len(evaluation.Nodes) {
		return decisionInvalid("evaluation nodes must exactly match the snapshot pre-order")
	}
	if result != evaluation.Result {
		return decisionInvalid("the evaluation root result must equal the first node result")
	}
	return nil
}

func validateConditionEvaluationNode(
	node *AutomationCondition,
	evaluatedAt time.Time,
	nodes []AutomationConditionNodeResult,
	cursor *int,
) (AutomationConditionResult, error) {
	if *cursor >= len(nodes) {
		return "", decisionInvalid("evaluation is missing a snapshot node")
	}
	recorded := nodes[*cursor]
	if recorded.ID != node.ID {
		return "", decisionInvalid("evaluation node IDs must match the snapshot pre-order")
	}
	*cursor++
	if !recorded.Result.isKnown() {
		return "", decisionInvalid("evaluation node result must be true, false, or unknown")
	}
	switch node.Kind {
	case AutomationConditionEntityState:
		return recorded.Result, validateConditionLeafEvidence(node, evaluatedAt, recorded)
	case AutomationConditionAll, AutomationConditionAny:
		return validateGroupEvaluation(node, evaluatedAt, nodes, cursor, recorded)
	case AutomationConditionNot:
		return validateNotEvaluation(node, evaluatedAt, nodes, cursor, recorded)
	default:
		return "", decisionInvalid(fmt.Sprintf("snapshot node %q has unknown kind %q", node.ID, node.Kind))
	}
}

// validateGroupEvaluation checks one all/any node against its recorded children.
func validateGroupEvaluation(
	node *AutomationCondition,
	evaluatedAt time.Time,
	nodes []AutomationConditionNodeResult,
	cursor *int,
	recorded AutomationConditionNodeResult,
) (AutomationConditionResult, error) {
	if err := validateCompositeNodeEvidence(recorded); err != nil {
		return "", err
	}
	childResults := make([]AutomationConditionResult, 0, len(node.Children))
	for index := range node.Children {
		child, err := validateConditionEvaluationNode(&node.Children[index], evaluatedAt, nodes, cursor)
		if err != nil {
			return "", err
		}
		childResults = append(childResults, child)
	}
	expected := combineConditionGroup(node.Kind, childResults)
	if recorded.Result != expected {
		return "", decisionInvalid("a composite result must agree with its recorded children")
	}
	return expected, nil
}

// validateNotEvaluation checks one not node against its recorded child.
func validateNotEvaluation(
	node *AutomationCondition,
	evaluatedAt time.Time,
	nodes []AutomationConditionNodeResult,
	cursor *int,
	recorded AutomationConditionNodeResult,
) (AutomationConditionResult, error) {
	if err := validateCompositeNodeEvidence(recorded); err != nil {
		return "", err
	}
	child, err := validateConditionEvaluationNode(node.Child, evaluatedAt, nodes, cursor)
	if err != nil {
		return "", err
	}
	expected := negateConditionResult(child)
	if recorded.Result != expected {
		return "", decisionInvalid("a not result must negate its recorded child")
	}
	return expected, nil
}

// validateCompositeNodeEvidence rejects fabricated leaf evidence on a composite
// node: composites retain only ID and result.
func validateCompositeNodeEvidence(recorded AutomationConditionNodeResult) error {
	if recorded.UnknownReason != nil || recorded.SelectedValue != nil ||
		recorded.ObservationID != nil || recorded.ObservedAt != nil {
		return decisionInvalid("a composite node carries no Entity evidence")
	}
	return nil
}

// validateConditionLeafEvidence enforces the leaf evidence rules of §2.6. Every
// rule needs no newer State: it validates only the retained evidence shape
// against the leaf's snapshot and the evaluation time.
func validateConditionLeafEvidence(
	node *AutomationCondition,
	evaluatedAt time.Time,
	recorded AutomationConditionNodeResult,
) error {
	if (recorded.ObservationID == nil) != (recorded.ObservedAt == nil) {
		return decisionInvalid("Observation ID and observed time are present together")
	}
	if recorded.ObservationID != nil {
		if _, err := devices.ParseObservationID(string(*recorded.ObservationID)); err != nil {
			return decisionInvalid("Observation ID is not canonical")
		}
	}
	if recorded.Result != AutomationConditionUnknown {
		return validateDefiniteLeafEvidence(node, evaluatedAt, recorded)
	}
	if recorded.UnknownReason == nil || !recorded.UnknownReason.isKnown() {
		return decisionInvalid("an unknown leaf requires exactly one listed reason")
	}
	return validateUnknownLeafEvidence(node, evaluatedAt, recorded, *recorded.UnknownReason)
}

// validateDefiniteLeafEvidence requires a true or false leaf to carry its
// selected value, Observation identity, and evidence time, with no reason. It
// then re-derives the result from the retained selection and the snapshot's
// operator/operand: the selected value must be comparable, its exact comparison
// must equal the recorded result, and bounded evidence must not be the future or
// expired case that would have preempted a definite result.
func validateDefiniteLeafEvidence(
	node *AutomationCondition,
	evaluatedAt time.Time,
	recorded AutomationConditionNodeResult,
) error {
	if recorded.UnknownReason != nil {
		return decisionInvalid("a true or false leaf carries no unknown reason")
	}
	if recorded.SelectedValue == nil || recorded.ObservationID == nil || recorded.ObservedAt == nil {
		return decisionInvalid("a true or false leaf requires a selected value and Observation evidence")
	}
	if reason, bounded := leafFreshnessReason(node, evaluatedAt, recorded); bounded {
		return decisionInvalid(fmt.Sprintf(
			"a true or false leaf with %s evidence must be unknown", reason,
		))
	}
	return validateExactLeafComparison(node, recorded)
}

// validateExactLeafComparison re-derives one definite leaf result from the
// retained selected value and the snapshot's operator and operand. Only exact
// comparison is accepted: a compatible selection whose verdict disagrees with
// the recorded result is fabricated evidence.
func validateExactLeafComparison(node *AutomationCondition, recorded AutomationConditionNodeResult) error {
	selected, operand, err := decodeLeafComparison(node, recorded)
	if err != nil {
		return err
	}
	if !conditionComparisonCompatible(node.EntityState.Operator, selected, operand) {
		return decisionInvalid("a true or false leaf requires a compatible selected value")
	}
	matched, err := compareJSONValues(node.EntityState.Operator, selected, operand)
	if err != nil {
		return decisionInvalid("a selected value cannot be compared")
	}
	if conditionResultFromMatch(matched) != recorded.Result {
		return decisionInvalid("a true or false leaf result must equal its exact comparison")
	}
	return nil
}

// validateIncompatibleLeafSelection requires one type_mismatch leaf to have
// actually selected a value that cannot be compared with the snapshot operand,
// so an unknown leaf cannot mislabel a comparable selection.
func validateIncompatibleLeafSelection(node *AutomationCondition, recorded AutomationConditionNodeResult) error {
	selected, operand, err := decodeLeafComparison(node, recorded)
	if err != nil {
		return err
	}
	if conditionComparisonCompatible(node.EntityState.Operator, selected, operand) {
		return decisionInvalid("type_mismatch requires an incompatible selected value")
	}
	return nil
}

// decodeLeafComparison decodes the retained selected value and the snapshot
// operand for one entity_state leaf. Both must be exactly one JSON value.
func decodeLeafComparison(node *AutomationCondition, recorded AutomationConditionNodeResult) (any, any, error) {
	if node.EntityState == nil {
		return nil, nil, decisionInvalid("leaf evidence requires an entity_state snapshot leaf")
	}
	selected, err := decodeJSONValue(recorded.SelectedValue)
	if err != nil {
		return nil, nil, decisionInvalid("a selected value must be exactly one JSON value")
	}
	operand, err := decodeJSONValue(node.EntityState.Operand)
	if err != nil {
		return nil, nil, decisionInvalid("a condition operand must be exactly one JSON value")
	}
	return selected, operand, nil
}

// leafFreshnessReason reports the freshness reason the leaf's retained
// Observation time demands at the evaluation time. It is false when no age
// bound applies or the evidence is inside the bound.
func leafFreshnessReason(
	node *AutomationCondition,
	evaluatedAt time.Time,
	recorded AutomationConditionNodeResult,
) (AutomationConditionUnknownReason, bool) {
	if node.EntityState == nil || recorded.ObservedAt == nil {
		return "", false
	}
	return boundedEvidenceUnknownReason(*node.EntityState, *recorded.ObservedAt, evaluatedAt)
}

// validateUnknownLeafEvidence requires exactly the evidence one unknown reason
// permits: missing Entity or State carries none, pointer_missing carries
// Observation evidence only, type_mismatch additionally requires an actually
// incompatible selected value, and the freshness reasons must be possible for the
// leaf's configured age bound at the evaluation time.
func validateUnknownLeafEvidence(
	node *AutomationCondition,
	evaluatedAt time.Time,
	recorded AutomationConditionNodeResult,
	reason AutomationConditionUnknownReason,
) error {
	switch reason {
	case AutomationConditionUnknownEntityMissing, AutomationConditionUnknownStateMissing:
		if recorded.SelectedValue != nil || recorded.ObservationID != nil || recorded.ObservedAt != nil {
			return decisionInvalid("missing Entity or State carries no value or Observation evidence")
		}
	case AutomationConditionUnknownPointerMissing:
		// The full State the pointer failed against is intentionally not retained,
		// so the failed selection itself cannot be re-derived here.
		if recorded.SelectedValue != nil {
			return decisionInvalid("pointer_missing carries no selected value")
		}
		if recorded.ObservationID == nil || recorded.ObservedAt == nil {
			return decisionInvalid("pointer_missing requires Observation evidence")
		}
		if err := rejectPreemptedFreshnessReason(node, evaluatedAt, recorded, reason); err != nil {
			return err
		}
	case AutomationConditionUnknownTypeMismatch:
		if recorded.ObservationID == nil || recorded.ObservedAt == nil {
			return decisionInvalid("comparable leaf evidence requires Observation evidence")
		}
		if recorded.SelectedValue == nil {
			return decisionInvalid("type_mismatch requires the selected value")
		}
		if err := rejectPreemptedFreshnessReason(node, evaluatedAt, recorded, reason); err != nil {
			return err
		}
		return validateIncompatibleLeafSelection(node, recorded)
	case AutomationConditionUnknownEvidenceInFuture:
		return validateBoundedEvidenceReason(node, evaluatedAt, recorded, true)
	case AutomationConditionUnknownEvidenceExpired:
		return validateBoundedEvidenceReason(node, evaluatedAt, recorded, false)
	default:
		return decisionInvalid("unknown leaf has an unlisted reason")
	}
	return nil
}

// rejectPreemptedFreshnessReason rejects a pointer or type failure recorded for
// bounded evidence that is actually future or expired. Freshness reasons take
// precedence over pointer and type failures, so such a leaf must record the
// corresponding freshness reason instead.
func rejectPreemptedFreshnessReason(
	node *AutomationCondition,
	evaluatedAt time.Time,
	recorded AutomationConditionNodeResult,
	reason AutomationConditionUnknownReason,
) error {
	if freshness, bounded := leafFreshnessReason(node, evaluatedAt, recorded); bounded {
		return decisionInvalid(fmt.Sprintf(
			"%s evidence must record %s instead", reason, freshness,
		))
	}
	return nil
}

// validateBoundedEvidenceReason rejects an impossible freshness reason. The leaf
// must carry its configured age bound and Observation evidence, the evidence time
// must actually be future or past the bound relative to evaluated_at, and the
// exact bound stays allowed. The selected value is optional because a freshness
// reason takes precedence over an unresolved pointer, but a present selection
// must still be exactly one JSON value.
func validateBoundedEvidenceReason(
	node *AutomationCondition,
	evaluatedAt time.Time,
	recorded AutomationConditionNodeResult,
	future bool,
) error {
	if node.Kind != AutomationConditionEntityState || node.EntityState == nil ||
		node.EntityState.MaxAgeSeconds == nil {
		return decisionInvalid("a freshness reason requires a max_age_seconds leaf")
	}
	if recorded.ObservationID == nil || recorded.ObservedAt == nil {
		return decisionInvalid("bounded evidence requires Observation evidence")
	}
	if recorded.SelectedValue != nil {
		if _, err := decodeJSONValue(recorded.SelectedValue); err != nil {
			return decisionInvalid("a selected value must be exactly one JSON value")
		}
	}
	age := evaluatedAt.UTC().Sub(recorded.ObservedAt.UTC())
	if future {
		if age >= 0 {
			return decisionInvalid("evidence_in_future requires evidence observed after evaluated_at")
		}
		return nil
	}
	if age <= time.Duration(*node.EntityState.MaxAgeSeconds)*time.Second {
		return decisionInvalid("evidence_expired requires evidence older than max_age_seconds")
	}
	return nil
}

// automationConditionEvaluationFromJSON maps one persisted evaluation to its
// domain form, normalizing evidence times to UTC so decode is canonical. An
// explicit JSON null on a non-evidence member is rejected before it can be
// mistaken for an absent member.
func automationConditionEvaluationFromJSON(
	value automationConditionEvaluationJSON,
) (AutomationConditionEvaluation, error) {
	evaluation := AutomationConditionEvaluation{
		EvaluatedAt: value.EvaluatedAt.UTC(),
		Result:      value.Result,
		Nodes:       make([]AutomationConditionNodeResult, 0, len(value.Nodes)),
	}
	for _, node := range value.Nodes {
		decoded, err := node.toDomain()
		if err != nil {
			return AutomationConditionEvaluation{}, err
		}
		evaluation.Nodes = append(evaluation.Nodes, decoded)
	}
	return evaluation, nil
}

// toDomain converts one persisted node. An explicit null placeholder on
// unknown_reason, observation_id, or observed_at is rejected; selected_value
// keeps JSON null as real evidence.
func (node automationConditionNodeJSON) toDomain() (AutomationConditionNodeResult, error) {
	decoded := AutomationConditionNodeResult{
		ID:            node.ID,
		Result:        node.Result,
		SelectedValue: node.SelectedValue,
	}
	reason, err := decodeOptionalConditionMember[AutomationConditionUnknownReason](
		node.UnknownReason, "condition node unknown_reason",
	)
	if err != nil {
		return AutomationConditionNodeResult{}, err
	}
	decoded.UnknownReason = reason
	observationID, err := decodeOptionalConditionMember[devices.ObservationID](
		node.ObservationID, "condition node observation_id",
	)
	if err != nil {
		return AutomationConditionNodeResult{}, err
	}
	decoded.ObservationID = observationID
	observedAt, err := decodeOptionalConditionMember[time.Time](
		node.ObservedAt, "condition node observed_at",
	)
	if err != nil {
		return AutomationConditionNodeResult{}, err
	}
	if observedAt != nil {
		normalized := observedAt.UTC()
		decoded.ObservedAt = &normalized
	}
	return decoded, nil
}

// encodeAutomationConditionEvaluation renders one evaluation in the strict
// persisted shape. Pointer members are marshaled to raw JSON so an absent member
// and an explicit null stay distinct on the wire.
func encodeAutomationConditionEvaluation(evaluation AutomationConditionEvaluation) (json.RawMessage, error) {
	encoded := automationConditionEvaluationJSON{
		EvaluatedAt: evaluation.EvaluatedAt.UTC(),
		Result:      evaluation.Result,
		Nodes:       make([]automationConditionNodeJSON, 0, len(evaluation.Nodes)),
	}
	for _, node := range evaluation.Nodes {
		item := automationConditionNodeJSON{
			ID:            node.ID,
			Result:        node.Result,
			SelectedValue: node.SelectedValue,
		}
		if node.UnknownReason != nil {
			raw, err := json.Marshal(*node.UnknownReason)
			if err != nil {
				return nil, fmt.Errorf("%w: unknown reason cannot be encoded: %w", ErrInvalidAutomation, err)
			}
			item.UnknownReason = raw
		}
		if node.ObservationID != nil {
			raw, err := json.Marshal(*node.ObservationID)
			if err != nil {
				return nil, fmt.Errorf("%w: Observation ID cannot be encoded: %w", ErrInvalidAutomation, err)
			}
			item.ObservationID = raw
		}
		if node.ObservedAt != nil {
			raw, err := json.Marshal(node.ObservedAt.UTC())
			if err != nil {
				return nil, fmt.Errorf("%w: observed time cannot be encoded: %w", ErrInvalidAutomation, err)
			}
			item.ObservedAt = raw
		}
		encoded.Nodes = append(encoded.Nodes, item)
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: condition evaluation cannot be encoded: %w", ErrInvalidAutomation, err)
	}
	return raw, nil
}

// decodeConditionEvaluationJSON strictly decodes one optional evaluation member.
// An explicit null is rejected instead of being treated as absent.
func decodeConditionEvaluationJSON(raw json.RawMessage) (*automationConditionEvaluationJSON, error) {
	if isExplicitJSONNull(raw) {
		return nil, decisionInvalid("condition decision evaluation must not be null")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil //nolint:nilnil // An absent evaluation is a valid not_evaluated/bypassed/snapshot-only decision.
	}
	var evaluation automationConditionEvaluationJSON
	if err := decodeStrictJSONObject(raw, &evaluation); err != nil {
		return nil, decisionInvalid("condition decision evaluation is not a strict object")
	}
	return &evaluation, nil
}

// decodeOptionalConditionMember decodes one optional decision member that must be
// absent or a non-null JSON value of T. An explicit null is a placeholder, not
// an omitted member, and is rejected with a fixed error.
func decodeOptionalConditionMember[T any](raw json.RawMessage, field string) (*T, error) {
	if isExplicitJSONNull(raw) {
		return nil, decisionInvalid(field + " must not be null")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil //nolint:nilnil // An absent optional member is valid; present value is the pointer.
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, decisionInvalid(field + " is malformed")
	}
	return &value, nil
}

// isExplicitJSONNull reports whether one raw JSON member is the literal null,
// which an omitted member never is.
func isExplicitJSONNull(raw json.RawMessage) bool {
	return len(raw) > 0 && bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// conditionsEqual compares two optional Condition trees by their canonical
// persisted shape, so a decision snapshot can be proven identical to a definition
// snapshot regardless of pointer identity or operand byte aliasing.
func conditionsEqual(left, right *AutomationCondition) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return reflect.DeepEqual(
		encodeAutomationCondition(*left),
		encodeAutomationCondition(*right),
	)
}

// decodeStrictJSONObject decodes exactly one JSON object, rejects unknown fields
// and trailing content, and never silently ignores a malformed payload.
func decodeStrictJSONObject(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing content after JSON object")
	}
	return nil
}

func decisionInvalid(message string) error {
	return fmt.Errorf("%w: condition decision: %s", ErrInvalidAutomation, message)
}
