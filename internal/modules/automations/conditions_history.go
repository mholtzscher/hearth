package automations

import (
	"bytes"
	"encoding/json"
	"fmt"
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
// JSON null.
type automationConditionNodeJSON struct {
	ID            ConditionID     `json:"id"`
	Result        ConditionResult `json:"result"`
	UnknownReason json.RawMessage `json:"unknown_reason,omitempty"`
	SelectedValue json.RawMessage `json:"selected_value,omitempty"`
	ObservationID json.RawMessage `json:"observation_id,omitempty"`
	ObservedAt    json.RawMessage `json:"observed_at,omitempty"`
}

// EncodeConditionDecision renders one decision in the strict persisted shape.
// bypass_requested is derived from the mode: true if and only if bypassed.
func EncodeConditionDecision(decision ConditionDecision) (json.RawMessage, error) {
	mode := decision.DecisionMode()
	bypass := decision.BypassRequested()
	value := automationConditionDecisionJSON{
		Mode:            &mode,
		BypassRequested: &bypass,
	}
	if snapshot := decision.DecisionSnapshot(); snapshot != nil {
		rawSnapshot, err := json.Marshal(encodeAutomationCondition(*snapshot))
		if err != nil {
			return nil, fmt.Errorf("%w: condition snapshot cannot be encoded: %w", ErrInvalidAutomation, err)
		}
		value.Snapshot = rawSnapshot
	}
	if evaluation := decision.DecisionEvaluation(); evaluation != nil {
		rawEvaluation, err := encodeAutomationConditionEvaluation(*evaluation)
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

// DecodeConditionDecision decodes one persisted decision, rejecting malformed
// payloads as permanent [ErrInvalidAutomation] errors. Snapshot and evaluation
// presence must match the mode; bypass_requested is derived from the mode and
// ignored on read, so decisions recorded before the flag became derived still
// decode.
func DecodeConditionDecision(raw json.RawMessage) (ConditionDecision, error) {
	var value automationConditionDecisionJSON
	if err := DecodeStrictJSONObject(raw, &value); err != nil {
		return nil, invalid("condition decision: condition decision is not a strict object")
	}
	if value.Mode == nil {
		return nil, invalid("condition decision: condition decision requires an explicit mode")
	}
	snapshot, err := decodeOptionalDecisionSnapshot(value.Snapshot)
	if err != nil {
		return nil, err
	}
	evaluationJSON, err := decodeConditionEvaluationJSON(value.Evaluation)
	if err != nil {
		return nil, err
	}
	switch *value.Mode {
	case ConditionDecisionNotConfigured:
		if snapshot != nil || evaluationJSON != nil {
			return nil, invalid("condition decision: not_configured carries a snapshot or evaluation")
		}
		return NotConfiguredDecision(), nil
	case ConditionDecisionNotEvaluated, ConditionDecisionBypassed:
		if snapshot == nil || evaluationJSON != nil {
			return nil, invalid("condition decision: mode requires a snapshot and no evaluation")
		}
		if *value.Mode == ConditionDecisionNotEvaluated {
			return NotEvaluatedDecision(*snapshot), nil
		}
		return BypassedDecision(*snapshot), nil
	case ConditionDecisionEvaluated:
		if snapshot == nil || evaluationJSON == nil {
			return nil, invalid("condition decision: evaluated requires a snapshot and evaluation")
		}
		evaluation, evaluationErr := automationConditionEvaluationFromJSON(*evaluationJSON)
		if evaluationErr != nil {
			return nil, evaluationErr
		}
		return EvaluatedDecision(*snapshot, evaluation), nil
	default:
		return nil, invalid("condition decision: unknown mode %q", *value.Mode)
	}
}

// decodeOptionalDecisionSnapshot decodes one optional snapshot member that must
// be absent or a non-null Condition tree.
func decodeOptionalDecisionSnapshot(raw json.RawMessage) (*Condition, error) {
	if isExplicitJSONNull(raw) {
		return nil, invalid("condition decision: condition decision snapshot must not be null")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil //nolint:nilnil // An absent snapshot is a valid not_configured decision.
	}
	var snapshotJSON automationConditionJSON
	if bindErr := json.Unmarshal(raw, &snapshotJSON); bindErr != nil {
		return nil, invalid("condition decision: condition snapshot cannot be bound")
	}
	snapshot := automationConditionFromJSON(snapshotJSON)
	return &snapshot, nil
}

// ValidateRunConditionDecision checks one Run's decision against its admission
// source and outcome. Envelope coherence holds by construction; only the
// record-kind rules remain.
func ValidateRunConditionDecision(run Run) error {
	decision := run.ConditionDecision
	switch decision.DecisionMode() {
	case ConditionDecisionNotConfigured, ConditionDecisionEvaluated:
	case ConditionDecisionBypassed:
		if run.Source != RunSourceManual {
			return invalid("condition decision: only a manual Run may bypass conditions")
		}
	case ConditionDecisionNotEvaluated:
		return invalid("condition decision: a Run never records a not_evaluated decision")
	default:
		return invalid("condition decision: unknown mode %q", decision.DecisionMode())
	}
	if evaluation := decision.DecisionEvaluation(); evaluation != nil && evaluation.Result != ConditionTrue {
		return invalid("condition decision: an evaluated Run requires a true root result")
	}
	return nil
}

// ValidateSkipConditionDecision checks one Skip's decision against its reason. A
// Skip never records a bypass; a condition Skip must have evaluated false or
// unknown to match its reason.
func ValidateSkipConditionDecision(skip Skip) error {
	decision := skip.ConditionDecision
	if decision.DecisionMode() == ConditionDecisionBypassed {
		return invalid("condition decision: a Skip is never a bypass")
	}
	switch skip.Reason {
	case SkipStaleFact, SkipBusy:
		if decision.DecisionMode() == ConditionDecisionEvaluated {
			return invalid("condition decision: a stale or busy Skip does not evaluate conditions")
		}
	case SkipConditionsFalse, SkipConditionsUnknown:
		if decision.DecisionMode() != ConditionDecisionEvaluated {
			return invalid("condition decision: a condition Skip requires an evaluated decision")
		}
		want := ConditionFalse
		if skip.Reason == SkipConditionsUnknown {
			want = ConditionUnknown
		}
		if decision.DecisionEvaluation().Result != want {
			return invalid("condition decision: a condition Skip result matches its reason")
		}
	default:
		return invalid("condition decision: unknown skip reason %q", skip.Reason)
	}
	return nil
}

// automationConditionEvaluationFromJSON maps one persisted evaluation to its
// domain form, normalizing evidence times to UTC.
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

// encodeAutomationConditionEvaluation renders one evaluation in the strict persisted shape.
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
func decodeConditionEvaluationJSON(raw json.RawMessage) (*automationConditionEvaluationJSON, error) {
	if isExplicitJSONNull(raw) {
		return nil, invalid("condition decision: condition decision evaluation must not be null")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil //nolint:nilnil // An absent evaluation is a valid not_evaluated/bypassed/snapshot-only decision.
	}
	var evaluation automationConditionEvaluationJSON
	if err := DecodeStrictJSONObject(raw, &evaluation); err != nil {
		return nil, invalid("condition decision: condition decision evaluation is not a strict object")
	}
	return &evaluation, nil
}

// decodeOptionalConditionMember decodes one optional decision member that must be
// absent or a non-null JSON value of T.
func decodeOptionalConditionMember[T any](raw json.RawMessage, field string) (*T, error) {
	if isExplicitJSONNull(raw) {
		return nil, invalid("condition decision: %s must not be null", field)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil //nolint:nilnil // An absent optional member is valid; present value is the pointer.
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, invalid("condition decision: %s is malformed", field)
	}
	return &value, nil
}

// isExplicitJSONNull reports whether one raw JSON member is the literal null.
func isExplicitJSONNull(raw json.RawMessage) bool {
	return len(raw) > 0 && bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
