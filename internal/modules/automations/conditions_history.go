package automations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// automationConditionDecisionJSON is the strict persisted decision shape. Every
// field is snake_case and family-inapplicable fields stay absent. Mode and
// BypassRequested are pointers so a missing or JSON-null scalar is rejected
// instead of silently decoding to its zero value.
type automationConditionDecisionJSON struct {
	Mode            *ConditionDecisionMode `json:"mode"`
	BypassRequested *bool                  `json:"bypass_requested"`
	Snapshot        json.RawMessage        `json:"snapshot,omitempty"`
	Evaluation      json.RawMessage        `json:"evaluation,omitempty"`
}

type automationConditionEvaluationJSON struct {
	EvaluatedAt time.Time                     `json:"evaluated_at"`
	Result      ConditionResult               `json:"result"`
	Nodes       []automationConditionNodeJSON `json:"nodes"`
}

// automationConditionNodeJSON keeps selected JSON null distinct from missing:
// SelectedValue is absent for "not selected" and the bytes "null" for a selected
// JSON null. The other optional members are raw so an explicit JSON null is
// distinguishable from an absent member and rejected as a placeholder.
type automationConditionNodeJSON struct {
	ID            ConditionID     `json:"id"`
	Result        ConditionResult `json:"result"`
	UnknownReason json.RawMessage `json:"unknown_reason,omitempty"`
	SelectedValue json.RawMessage `json:"selected_value,omitempty"`
	ObservationID json.RawMessage `json:"observation_id,omitempty"`
	ObservedAt    json.RawMessage `json:"observed_at,omitempty"`
}

// EncodeConditionDecision renders one decision in the strict persisted
// shape. It is a pure encoder: the write path validates a decision before
// calling it, and no partial or inconsistent decision is written.
func EncodeConditionDecision(decision ConditionDecision) (json.RawMessage, error) {
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

// DecodeConditionDecision decodes one persisted decision. Every
// automation_history row carries an explicit decision object, so an empty or
// missing payload is malformed rather than an implicit not_configured mode.
// Malformed decision JSON is a permanent [ErrInvalidAutomation]. Retained
// snapshot and evaluation evidence is decoded and trusted, never re-proven
// against the evaluator.
func DecodeConditionDecision(raw json.RawMessage) (ConditionDecision, error) {
	var value automationConditionDecisionJSON
	if err := decodeStrictJSONObject(raw, &value); err != nil {
		return ConditionDecision{}, decisionInvalid("condition decision is not a strict object")
	}
	if value.Mode == nil || value.BypassRequested == nil {
		return ConditionDecision{}, decisionInvalid(
			"condition decision requires an explicit mode and bypass_requested",
		)
	}
	if isExplicitJSONNull(value.Snapshot) {
		return ConditionDecision{}, decisionInvalid("condition decision snapshot must not be null")
	}
	if isExplicitJSONNull(value.Evaluation) {
		return ConditionDecision{}, decisionInvalid("condition decision evaluation must not be null")
	}
	decision := ConditionDecision{
		Mode:            *value.Mode,
		BypassRequested: *value.BypassRequested,
	}
	if len(bytes.TrimSpace(value.Snapshot)) > 0 {
		var snapshotJSON automationConditionJSON
		if bindErr := json.Unmarshal(value.Snapshot, &snapshotJSON); bindErr != nil {
			return ConditionDecision{}, decisionInvalid("condition snapshot cannot be bound")
		}
		snapshot := automationConditionFromJSON(snapshotJSON)
		decision.Snapshot = &snapshot
	}
	evaluationJSON, decodeErr := decodeConditionEvaluationJSON(value.Evaluation)
	if decodeErr != nil {
		return ConditionDecision{}, decodeErr
	}
	if evaluationJSON != nil {
		evaluation, evaluationErr := automationConditionEvaluationFromJSON(*evaluationJSON)
		if evaluationErr != nil {
			return ConditionDecision{}, evaluationErr
		}
		decision.Evaluation = &evaluation
	}
	return decision, nil
}

// ValidateConditionDecision enforces the decision envelope invariants
// that do not depend on the enclosing Run or Skip: closed mode, snapshot and
// evaluation presence per mode, and bypass coherence with mode. The evaluator
// produces evaluation evidence once at write time; this validator does not
// re-derive it from the retained snapshot.
func ValidateConditionDecision(decision ConditionDecision) error {
	if !decision.Mode.isKnown() {
		return decisionInvalid(fmt.Sprintf("unknown mode %q", decision.Mode))
	}
	switch decision.Mode {
	case ConditionDecisionNotConfigured:
		if decision.Snapshot != nil || decision.Evaluation != nil {
			return decisionInvalid("not_configured carries a snapshot or evaluation")
		}
	case ConditionDecisionNotEvaluated, ConditionDecisionBypassed:
		if decision.Snapshot == nil || decision.Evaluation != nil {
			return decisionInvalid("mode requires a snapshot and no evaluation")
		}
	case ConditionDecisionEvaluated:
		if decision.Snapshot == nil || decision.Evaluation == nil {
			return decisionInvalid("evaluated requires a snapshot and evaluation")
		}
	default:
		return decisionInvalid(fmt.Sprintf("unknown mode %q", decision.Mode))
	}
	if decision.Snapshot != nil && decision.BypassRequested != (decision.Mode == ConditionDecisionBypassed) {
		return decisionInvalid("with configured conditions the bypass request matches the bypassed mode")
	}
	return nil
}

// ValidateRunConditionDecision checks one Run's decision against its admission
// source and outcome: a Run never records not_evaluated, bypass is manual only,
// and an evaluated Run is admitted on a true root.
func ValidateRunConditionDecision(run Run) error {
	decision := run.ConditionDecision
	if err := ValidateConditionDecision(decision); err != nil {
		return err
	}
	if decision.BypassRequested && run.Source != RunSourceManual {
		return decisionInvalid("only a manual Run may request a bypass")
	}
	switch decision.Mode {
	case ConditionDecisionNotEvaluated:
		return decisionInvalid("a Run never records a not_evaluated decision")
	case ConditionDecisionBypassed:
		if run.Source != RunSourceManual {
			return decisionInvalid("only a manual Run may bypass conditions")
		}
	case ConditionDecisionEvaluated:
		if decision.Evaluation.Result != ConditionTrue {
			return decisionInvalid("an evaluated Run requires a true root result")
		}
	case ConditionDecisionNotConfigured:
	default:
		return decisionInvalid(fmt.Sprintf("unknown mode %q", decision.Mode))
	}
	return nil
}

// ValidateSkipConditionDecision checks one Skip's decision against its reason. A
// Skip never requests or records a bypass; stale and busy never evaluate
// conditions, and a condition Skip must have evaluated false or unknown to match
// its reason. This is the validator that rejects fabricated provenance.
func ValidateSkipConditionDecision(skip Skip) error {
	decision := skip.ConditionDecision
	if err := ValidateConditionDecision(decision); err != nil {
		return err
	}
	if decision.Mode == ConditionDecisionBypassed || decision.BypassRequested {
		return decisionInvalid("a Skip is never a bypass")
	}
	switch skip.Reason {
	case SkipStaleFact, SkipBusy:
		if decision.Mode == ConditionDecisionEvaluated {
			return decisionInvalid("a stale or busy Skip does not evaluate conditions")
		}
	case SkipConditionsFalse, SkipConditionsUnknown:
		if decision.Mode != ConditionDecisionEvaluated {
			return decisionInvalid("a condition Skip requires an evaluated decision")
		}
		want := ConditionFalse
		if skip.Reason == SkipConditionsUnknown {
			want = ConditionUnknown
		}
		if decision.Evaluation.Result != want {
			return decisionInvalid("a condition Skip result matches its reason")
		}
	default:
		return decisionInvalid(fmt.Sprintf("unknown skip reason %q", skip.Reason))
	}
	return nil
}

// automationConditionEvaluationFromJSON maps one persisted evaluation to its
// domain form, normalizing evidence times to UTC so decode is canonical. An
// explicit JSON null on a non-evidence member is rejected before it can be
// mistaken for an absent member.
func automationConditionEvaluationFromJSON(
	value automationConditionEvaluationJSON,
) (ConditionEvaluation, error) {
	evaluation := ConditionEvaluation{
		EvaluatedAt: value.EvaluatedAt.UTC(),
		Result:      value.Result,
		Nodes:       make([]ConditionNodeResult, 0, len(value.Nodes)),
	}
	for _, node := range value.Nodes {
		decoded, err := node.toDomain()
		if err != nil {
			return ConditionEvaluation{}, err
		}
		evaluation.Nodes = append(evaluation.Nodes, decoded)
	}
	return evaluation, nil
}

// toDomain converts one persisted node. An explicit null placeholder on
// unknown_reason, observation_id, or observed_at is rejected; selected_value
// keeps JSON null as real evidence.
func (node automationConditionNodeJSON) toDomain() (ConditionNodeResult, error) {
	decoded := ConditionNodeResult{
		ID:            node.ID,
		Result:        node.Result,
		SelectedValue: node.SelectedValue,
	}
	reason, err := decodeOptionalConditionMember[ConditionUnknownReason](
		node.UnknownReason, "condition node unknown_reason",
	)
	if err != nil {
		return ConditionNodeResult{}, err
	}
	decoded.UnknownReason = reason
	observationID, err := decodeOptionalConditionMember[devices.ObservationID](
		node.ObservationID, "condition node observation_id",
	)
	if err != nil {
		return ConditionNodeResult{}, err
	}
	decoded.ObservationID = observationID
	observedAt, err := decodeOptionalConditionMember[time.Time](
		node.ObservedAt, "condition node observed_at",
	)
	if err != nil {
		return ConditionNodeResult{}, err
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
func encodeAutomationConditionEvaluation(evaluation ConditionEvaluation) (json.RawMessage, error) {
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
