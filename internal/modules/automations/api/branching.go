package api

import (
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// AutomationBranchDecisionBody contains the canonical concrete If or Choose
// result DTO. Its encoded evaluations preserve exact JSON evidence.
type AutomationBranchDecisionBody struct{ encoded json.RawMessage }

func (body AutomationBranchDecisionBody) MarshalJSON() ([]byte, error) {
	return body.encoded.MarshalJSON()
}

func branchDecisionBody(decision automations.BranchDecision) AutomationBranchDecisionBody {
	raw, err := automations.EncodeBranchDecision(decision)
	if err != nil {
		panic(fmt.Errorf("invalid retained branch decision: %w", err))
	}
	return AutomationBranchDecisionBody{encoded: raw}
}
