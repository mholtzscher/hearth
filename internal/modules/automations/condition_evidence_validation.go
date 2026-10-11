package automations

import (
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func validateConditionEvaluationShape(evaluation ConditionEvaluation) error {
	if evaluation.EvaluatedAt.IsZero() || !validConditionResult(evaluation.Result) ||
		len(evaluation.Nodes) < 1 || len(evaluation.Nodes) > automationConditionMaxNodes {
		return invalid("Condition evaluation requires bounded leaf evidence, a result, and time")
	}
	seen := make(map[ConditionID]bool)
	for _, node := range evaluation.Nodes {
		if !subjectSlugPattern.MatchString(string(node.ID)) || seen[node.ID] || !validConditionResult(node.Result()) {
			return invalid("Condition evaluation has invalid or duplicate leaf identities or results")
		}
		seen[node.ID] = true
		if err := validateConditionLeafEvidence(node); err != nil {
			return err
		}
	}
	return nil
}

func validConditionResult(result ConditionResult) bool {
	return result == ConditionTrue || result == ConditionFalse || result == ConditionUnknown
}

func validateConditionLeafEvidence(node ConditionNodeResult) error {
	switch evidence := node.Evidence.(type) {
	case TriggerMatchEvidence:
		return validateTriggerLeafShape(evidence)
	case KnownStateEvidence:
		if evidence.SelectedValue == nil {
			return invalid("known State requires selection")
		}
		if err := validateObservationEvidence(evidence.Observation); err != nil {
			return err
		}
		return validateSelectedValue(evidence.SelectedValue)
	case UnknownStateEvidence:
		if evidence.Observation != nil {
			if err := validateObservationEvidence(*evidence.Observation); err != nil {
				return err
			}
		}
		if err := validateSelectedValue(evidence.SelectedValue); err != nil {
			return err
		}
		if !stateUnknownHasValidShape(evidence) {
			return invalid("State unknown reason disagrees with evidence shape")
		}
		return nil
	default:
		return invalid("unsupported Condition evidence representation")
	}
}

func validateTriggerLeafShape(evidence TriggerMatchEvidence) error {
	ids := evidence.MatchedTriggerIDs
	if len(ids) > automationTriggerMaxCount {
		return invalid("Trigger intersection exceeds bound")
	}
	seen := make(map[TriggerID]bool)
	for _, id := range ids {
		if !subjectSlugPattern.MatchString(string(id)) || seen[id] {
			return invalid("Trigger evidence IDs must be valid and unique")
		}
		seen[id] = true
	}
	return nil
}

func validateObservationEvidence(observation ObservationEvidence) error {
	if _, err := devices.ParseObservationID(string(observation.ObservationID)); err != nil {
		return invalid("State leaf Observation identity is invalid")
	}
	if observation.ObservedAt.IsZero() {
		return invalid("State leaf Observation time is zero")
	}
	return nil
}

func validateSelectedValue(value json.RawMessage) error {
	if value != nil {
		if _, err := decodeJSONValue(value); err != nil {
			return invalid("State leaf selection must be exactly one JSON value")
		}
	}
	return nil
}

func stateUnknownHasValidShape(evidence UnknownStateEvidence) bool {
	switch evidence.Reason {
	case ConditionUnknownEntityMissing, ConditionUnknownStateMissing:
		return evidence.SelectedValue == nil && evidence.Observation == nil
	case ConditionUnknownPointerMissing:
		return evidence.SelectedValue == nil && evidence.Observation != nil
	case ConditionUnknownTypeMismatch:
		return evidence.SelectedValue != nil && evidence.Observation != nil
	case ConditionUnknownEvidenceInFuture, ConditionUnknownEvidenceExpired:
		return evidence.Observation != nil
	default:
		return false
	}
}
