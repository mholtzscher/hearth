package automations

import (
	"encoding/json"
	"fmt"
)

// conditionDecisionEvidenceJSON retains only the admission's own evidence.
// Configuration belongs to the immutable definition revision, not each Run.
type conditionDecisionEvidenceJSON struct {
	conditionDecisionHeaderJSON

	Evaluation json.RawMessage `json:"evaluation,omitempty"`
}

// EncodePreparedConditionDecisionEvidence encodes a Run's decision without its
// configured snapshot. It consumes a decision made from an unchanged normalized
// definition, which the caller must retain separately. Evidence is still checked.
func EncodePreparedConditionDecisionEvidence(decision ConditionDecision) (json.RawMessage, error) {
	switch decision.(type) {
	case notConfiguredDecision, notEvaluatedDecision, bypassedDecision, evaluatedDecision:
	default:
		return nil, invalid("condition decision: unsupported value representation")
	}
	mode, bypass := decision.DecisionMode(), decision.BypassRequested()
	header := conditionDecisionHeaderJSON{
		Mode: &mode, BypassRequested: &bypass,
	}
	value := conditionDecisionEvidenceJSON{conditionDecisionHeaderJSON: header}
	if evaluation := decision.DecisionEvaluation(); evaluation != nil {
		if err := rejectAdmissionTriggerEvidence(*evaluation); err != nil {
			return nil, err
		}
		raw, err := encodeAutomationConditionEvaluation(*evaluation)
		if err != nil {
			return nil, err
		}
		value.Evaluation = raw
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode Condition evidence: %w", err)
	}
	return raw, nil
}

// DecodeConditionDecisionEvidence assembles a strict decision from retained Run
// evidence and the referenced definition's Conditions. It rejects inline
// configuration and inconsistent configuration presence instead of inventing it.
func DecodeConditionDecisionEvidence(raw json.RawMessage, conditions *Condition) (ConditionDecision, error) {
	var value conditionDecisionEvidenceJSON
	if err := DecodeStrictJSONObject(raw, &value); err != nil {
		return nil, invalid("condition decision evidence: expected envelope and optional evaluation only")
	}
	if value.Mode == nil || ((*value.Mode == ConditionDecisionNotConfigured) != (conditions == nil)) {
		return nil, invalid("condition decision evidence: mode disagrees with definition Conditions")
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	if conditions != nil {
		snapshot, err := json.Marshal(encodeAutomationCondition(*conditions))
		if err != nil {
			return nil, fmt.Errorf("encode retained Conditions: %w", err)
		}
		document["snapshot"] = snapshot
	}
	assembled, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("assemble Condition decision: %w", err)
	}
	return DecodeConditionDecision(assembled)
}
