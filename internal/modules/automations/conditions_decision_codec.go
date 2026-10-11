package automations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type conditionDecisionHeaderJSON struct {
	Mode            *ConditionDecisionMode `json:"mode"`
	BypassRequested *bool                  `json:"bypass_requested"`
}

type configuredConditionDecisionJSON struct {
	conditionDecisionHeaderJSON

	Snapshot json.RawMessage `json:"snapshot"`
}

type evaluatedConditionDecisionJSON struct {
	conditionDecisionHeaderJSON

	Snapshot   json.RawMessage `json:"snapshot"`
	Evaluation json.RawMessage `json:"evaluation"`
}

type automationConditionEvaluationJSON struct {
	EvaluatedAt time.Time         `json:"evaluated_at"`
	Result      ConditionResult   `json:"result"`
	Nodes       []json.RawMessage `json:"nodes"`
}

// Concrete leaf DTOs distinguish known State, unknown State, and Trigger matches.
type conditionLeafHeaderJSON struct {
	ID     ConditionID     `json:"id"`
	Kind   ConditionKind   `json:"kind"`
	Result ConditionResult `json:"result"`
}
type knownStateEvidenceJSON struct {
	conditionLeafHeaderJSON

	ObservationID devices.ObservationID `json:"observation_id"`
	ObservedAt    time.Time             `json:"observed_at"`
	SelectedValue json.RawMessage       `json:"selected_value"`
}
type unknownStateEvidenceJSON struct {
	conditionLeafHeaderJSON

	UnknownReason ConditionUnknownReason `json:"unknown_reason"`
	ObservationID json.RawMessage        `json:"observation_id,omitempty"`
	ObservedAt    json.RawMessage        `json:"observed_at,omitempty"`
	SelectedValue json.RawMessage        `json:"selected_value,omitempty"`
}
type triggerMatchEvidenceJSON struct {
	conditionLeafHeaderJSON

	MatchedTriggerIDs *[]TriggerID `json:"matched_trigger_ids"`
}

// EncodeConditionDecision renders one decision in the strict persisted shape.
// bypass_requested is derived from the mode: true if and only if bypassed.
func EncodeConditionDecision(decision ConditionDecision) (json.RawMessage, error) {
	switch decision.(type) {
	case notConfiguredDecision, notEvaluatedDecision, bypassedDecision, evaluatedDecision:
	default:
		return nil, invalid("condition decision: unsupported value representation")
	}
	mode := decision.DecisionMode()
	bypass := decision.BypassRequested()
	header := conditionDecisionHeaderJSON{
		Mode:            &mode,
		BypassRequested: &bypass,
	}
	var snapshotJSON, evaluationJSON json.RawMessage
	if snapshot := decision.DecisionSnapshot(); snapshot != nil {
		if err := validateAutomationConditionTree(*snapshot); err != nil {
			return nil, err
		}
		rawSnapshot, err := json.Marshal(encodeAutomationCondition(*snapshot))
		if err != nil {
			return nil, fmt.Errorf("%w: condition snapshot cannot be encoded: %w", ErrInvalidAutomation, err)
		}
		snapshotJSON = rawSnapshot
	}
	if evaluation := decision.DecisionEvaluation(); evaluation != nil {
		if err := rejectAdmissionTriggerEvidence(*evaluation); err != nil {
			return nil, err
		}
		rawEvaluation, err := encodeAutomationConditionEvaluation(*evaluation)
		if err != nil {
			return nil, err
		}
		evaluationJSON = rawEvaluation
	}
	return marshalConditionDecision(decision, header, snapshotJSON, evaluationJSON)
}

func marshalConditionDecision(
	decision ConditionDecision, header conditionDecisionHeaderJSON, snapshot, evaluation json.RawMessage,
) (json.RawMessage, error) {
	var raw []byte
	var err error
	switch decision.(type) {
	case notConfiguredDecision:
		raw, err = json.Marshal(header)
	case notEvaluatedDecision, bypassedDecision:
		raw, err = json.Marshal(
			configuredConditionDecisionJSON{conditionDecisionHeaderJSON: header, Snapshot: snapshot},
		)
	case evaluatedDecision:
		raw, err = json.Marshal(evaluatedConditionDecisionJSON{
			conditionDecisionHeaderJSON: header, Snapshot: snapshot, Evaluation: evaluation,
		})
	default:
		return nil, invalid("condition decision: unsupported value representation")
	}
	if err != nil {
		return nil, fmt.Errorf("%w: condition decision cannot be encoded: %w", ErrInvalidAutomation, err)
	}
	return raw, nil
}

// DecodeConditionDecision decodes one persisted decision, rejecting malformed
// payloads as permanent [ErrInvalidAutomation] errors. Snapshot and evaluation
// presence and bypass_requested must match the mode.
func DecodeConditionDecision(raw json.RawMessage) (ConditionDecision, error) {
	var header conditionDecisionHeaderJSON
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, invalid("condition decision: condition decision is not a strict object")
	}
	if header.Mode == nil || header.BypassRequested == nil ||
		*header.BypassRequested != (*header.Mode == ConditionDecisionBypassed) {
		return nil, invalid("condition decision: explicit mode and consistent bypass_requested are required")
	}
	switch *header.Mode {
	case ConditionDecisionNotConfigured:
		if err := DecodeStrictJSONObject(raw, &header); err != nil {
			return nil, invalid("condition decision: not_configured must contain only its envelope")
		}
		return NotConfiguredDecision(), nil
	case ConditionDecisionNotEvaluated, ConditionDecisionBypassed:
		var value configuredConditionDecisionJSON
		if err := DecodeStrictJSONObject(raw, &value); err != nil {
			return nil, invalid("condition decision: configured mode requires only a snapshot")
		}
		snapshot, err := requiredDecisionSnapshot(value.Snapshot)
		if err != nil {
			return nil, err
		}
		if *header.Mode == ConditionDecisionNotEvaluated {
			return NotEvaluatedDecision(*snapshot), nil
		}
		return BypassedDecision(*snapshot), nil
	case ConditionDecisionEvaluated:
		return decodeEvaluatedDecision(raw)
	default:
		return nil, invalid("condition decision: unknown mode %q", *header.Mode)
	}
}

func requiredDecisionSnapshot(raw json.RawMessage) (*Condition, error) {
	snapshot, err := decodeOptionalDecisionSnapshot(raw)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, invalid("condition decision: snapshot is required")
	}
	return snapshot, nil
}

func decodeEvaluatedDecision(raw json.RawMessage) (ConditionDecision, error) {
	var value evaluatedConditionDecisionJSON
	if err := DecodeStrictJSONObject(raw, &value); err != nil {
		return nil, invalid("condition decision: evaluated mode requires a snapshot and evaluation")
	}
	snapshot, err := requiredDecisionSnapshot(value.Snapshot)
	if err != nil {
		return nil, err
	}
	evaluationJSON, err := decodeConditionEvaluationJSON(value.Evaluation)
	if err != nil {
		return nil, err
	}
	if evaluationJSON == nil {
		return nil, invalid("condition decision: evaluation is required")
	}
	evaluation, err := automationConditionEvaluationFromJSON(*evaluationJSON)
	if err != nil {
		return nil, err
	}
	if err = rejectAdmissionTriggerEvidence(evaluation); err != nil {
		return nil, err
	}
	return EvaluatedDecision(*snapshot, evaluation), nil
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
	document, err := decodeJSONValue(raw)
	if err != nil {
		return nil, invalid("condition decision: snapshot must be one JSON object")
	}
	if err = boundConditionJSON(document, 1, new(int)); err != nil {
		return nil, err
	}
	codec, err := automationDefinitionCodec()
	if err != nil {
		return nil, err
	}
	if err = codec.conditions.Validate(document); err != nil {
		return nil, invalid("condition decision: snapshot must satisfy the admission Condition schema")
	}
	var snapshotJSON automationConditionJSON
	if bindErr := json.Unmarshal(raw, &snapshotJSON); bindErr != nil {
		return nil, invalid("condition decision: condition snapshot cannot be bound")
	}
	snapshot := automationConditionFromJSON(snapshotJSON)
	normalized, err := NormalizeConditions(snapshot)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
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
		decoded, err := decodeConditionNode(node)
		if err != nil {
			return ConditionEvaluation{}, err
		}
		evaluation.Nodes = append(evaluation.Nodes, decoded)
	}
	if err := validateConditionEvaluationShape(evaluation); err != nil {
		return ConditionEvaluation{}, err
	}
	return evaluation, nil
}

// decodeConditionNode converts one persisted node. An explicit null placeholder on
// unknown_reason, observation_id, or observed_at is rejected; selected_value
// keeps JSON null as real evidence.
func decodeConditionNode(raw json.RawMessage) (ConditionNodeResult, error) {
	var header conditionLeafHeaderJSON
	if err := json.Unmarshal(raw, &header); err != nil {
		return ConditionNodeResult{}, invalid("condition leaf requires an object")
	}
	node := ConditionNodeResult{ID: header.ID}
	switch header.Kind {
	case ConditionTrigger:
		var value triggerMatchEvidenceJSON
		if DecodeStrictJSONObject(raw, &value) != nil || value.MatchedTriggerIDs == nil {
			return ConditionNodeResult{}, invalid("Trigger leaf requires a non-null matched_trigger_ids array")
		}
		node.Evidence = TriggerMatchEvidence{MatchedTriggerIDs: *value.MatchedTriggerIDs}
	case ConditionEntityState:
		if header.Result == ConditionUnknown {
			evidence, err := decodeUnknownStateEvidence(raw)
			if err != nil {
				return ConditionNodeResult{}, err
			}
			node.Evidence = evidence
		} else {
			var value knownStateEvidenceJSON
			if DecodeStrictJSONObject(raw, &value) != nil {
				return ConditionNodeResult{}, invalid("known State leaf requires exact State evidence")
			}
			node.Evidence = KnownStateEvidence{
				Matched: header.Result == ConditionTrue,
				Observation: ObservationEvidence{
					ObservationID: value.ObservationID,
					ObservedAt:    value.ObservedAt.UTC(),
				},
				SelectedValue: value.SelectedValue,
			}
		}
	case ConditionAll, ConditionAny, ConditionNot:
		return ConditionNodeResult{}, invalid("groups are not retained leaves")
	default:
		return ConditionNodeResult{}, invalid("unknown Condition leaf kind")
	}
	if node.Result() != header.Result {
		return ConditionNodeResult{}, invalid("leaf result disagrees with evidence")
	}
	if err := validateConditionLeafEvidence(node); err != nil {
		return ConditionNodeResult{}, err
	}
	return node, nil
}

func decodeUnknownStateEvidence(raw json.RawMessage) (UnknownStateEvidence, error) {
	var value unknownStateEvidenceJSON
	if DecodeStrictJSONObject(raw, &value) != nil {
		return UnknownStateEvidence{}, invalid("unknown State leaf requires exact evidence")
	}
	id, err := decodeOptionalConditionMember[devices.ObservationID](value.ObservationID, "observation_id")
	if err != nil {
		return UnknownStateEvidence{}, err
	}
	at, err := decodeOptionalConditionMember[time.Time](value.ObservedAt, "observed_at")
	if err != nil {
		return UnknownStateEvidence{}, err
	}
	if (id == nil) != (at == nil) {
		return UnknownStateEvidence{}, invalid("Observation evidence must be complete")
	}
	evidence := UnknownStateEvidence{Reason: value.UnknownReason, SelectedValue: value.SelectedValue}
	if id != nil {
		evidence.Observation = &ObservationEvidence{ObservationID: *id, ObservedAt: at.UTC()}
	}
	return evidence, nil
}

// encodeAutomationConditionEvaluation renders one evaluation in the strict persisted shape.
func encodeAutomationConditionEvaluation(evaluation ConditionEvaluation) (json.RawMessage, error) {
	if err := validateConditionEvaluationShape(evaluation); err != nil {
		return nil, err
	}
	encoded := automationConditionEvaluationJSON{
		EvaluatedAt: evaluation.EvaluatedAt.UTC(),
		Result:      evaluation.Result,
		Nodes:       make([]json.RawMessage, 0, len(evaluation.Nodes)),
	}
	for _, node := range evaluation.Nodes {
		item, err := encodeAutomationConditionNode(node)
		if err != nil {
			return nil, err
		}
		encoded.Nodes = append(encoded.Nodes, item)
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: condition evaluation cannot be encoded: %w", ErrInvalidAutomation, err)
	}
	return raw, nil
}

func encodeAutomationConditionNode(node ConditionNodeResult) (json.RawMessage, error) {
	if err := validateConditionLeafEvidence(node); err != nil {
		return nil, err
	}
	header := conditionLeafHeaderJSON{ID: node.ID, Kind: ConditionEntityState, Result: node.Result()}
	switch evidence := node.Evidence.(type) {
	case KnownStateEvidence:
		return marshalRetainedEvidence(knownStateEvidenceJSON{conditionLeafHeaderJSON: header,
			ObservationID: evidence.Observation.ObservationID, ObservedAt: evidence.Observation.ObservedAt.UTC(),
			SelectedValue: evidence.SelectedValue})
	case UnknownStateEvidence:
		value := unknownStateEvidenceJSON{
			conditionLeafHeaderJSON: header,
			UnknownReason:           evidence.Reason,
			SelectedValue:           evidence.SelectedValue,
		}
		if evidence.Observation != nil {
			value.ObservationID, _ = json.Marshal(evidence.Observation.ObservationID)
			var err error
			value.ObservedAt, err = marshalRetainedEvidence(evidence.Observation.ObservedAt.UTC())
			if err != nil {
				return nil, err
			}
		}
		return marshalRetainedEvidence(value)
	case TriggerMatchEvidence:
		header.Kind = ConditionTrigger
		ids := append([]TriggerID{}, evidence.MatchedTriggerIDs...)
		return marshalRetainedEvidence(
			triggerMatchEvidenceJSON{conditionLeafHeaderJSON: header, MatchedTriggerIDs: &ids},
		)
	default:
		return nil, invalid("unsupported Condition evidence representation")
	}
}

func rejectAdmissionTriggerEvidence(evaluation ConditionEvaluation) error {
	for _, node := range evaluation.Nodes {
		if _, trigger := node.Evidence.(TriggerMatchEvidence); trigger {
			return invalid("condition decision: Trigger evidence is invalid at admission")
		}
	}
	return nil
}

func marshalRetainedEvidence(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: retained evidence cannot be encoded: %w", ErrInvalidAutomation, err)
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
