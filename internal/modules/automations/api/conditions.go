package api

import (
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// AutomationConditionDecisionBody contains the canonical concrete decision DTO,
// including its mode-specific snapshot and concrete leaf evidence.
type AutomationConditionDecisionBody struct{ encoded json.RawMessage }

func (body AutomationConditionDecisionBody) MarshalJSON() ([]byte, error) {
	return body.encoded.MarshalJSON()
}

func conditionDecisionBody(decision automations.ConditionDecision) AutomationConditionDecisionBody {
	raw, err := automations.EncodeConditionDecision(decision)
	if err != nil {
		panic(fmt.Errorf("invalid retained Condition decision: %w", err))
	}
	return AutomationConditionDecisionBody{encoded: raw}
}
