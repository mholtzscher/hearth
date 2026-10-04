package automations

import (
	"encoding/json"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// ConditionEvidence records either State evidence or matched Trigger identities.
//
//sumtype:decl
type ConditionEvidence interface{ isConditionEvidence() }

// ObservationEvidence identifies the Observation underlying retained State.
type ObservationEvidence struct {
	ObservationID devices.ObservationID
	ObservedAt    time.Time
}

// KnownStateEvidence records a comparable selected value and its result.
type KnownStateEvidence struct {
	Matched       bool
	Observation   ObservationEvidence
	SelectedValue json.RawMessage
}

// UnknownStateEvidence explains why a State comparison could not be decided.
// A nil SelectedValue is absent; JSON null is a selected value.
type UnknownStateEvidence struct {
	Reason        ConditionUnknownReason
	Observation   *ObservationEvidence
	SelectedValue json.RawMessage
}

// TriggerMatchEvidence records the configured-order match intersection.
type TriggerMatchEvidence struct{ MatchedTriggerIDs []TriggerID }

func (KnownStateEvidence) isConditionEvidence()   {}
func (UnknownStateEvidence) isConditionEvidence() {}
func (TriggerMatchEvidence) isConditionEvidence() {}

// Result derives the leaf result from supported evidence values.
func (node ConditionNodeResult) Result() ConditionResult {
	switch evidence := node.Evidence.(type) {
	case KnownStateEvidence:
		return conditionResultFromMatch(evidence.Matched)
	case UnknownStateEvidence:
		return ConditionUnknown
	case TriggerMatchEvidence:
		return conditionResultFromMatch(len(evidence.MatchedTriggerIDs) > 0)
	default:
		return ""
	}
}
