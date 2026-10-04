package sqlite

import (
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// conditionSummaryFromColumns decodes materialized evidence only. Listing never
// consults the full snapshot or Condition decision document.
func conditionSummaryFromColumns(row dbsqlc.AutomationHistory) (automations.ConditionDecisionSummary, error) {
	mode := automations.ConditionDecisionMode(row.ConditionMode)
	wantBypass := int64(0)
	if mode == automations.ConditionDecisionBypassed {
		wantBypass = 1
	}
	if row.ConditionBypassed != wantBypass ||
		row.ConditionResult.Valid != (mode == automations.ConditionDecisionEvaluated) {
		return nil, lifecycleCorruption("inconsistent Condition summary columns")
	}
	switch mode {
	case automations.ConditionDecisionNotConfigured:
		return automations.NotConfiguredSummary{}, nil
	case automations.ConditionDecisionNotEvaluated:
		return automations.NotEvaluatedSummary{}, nil
	case automations.ConditionDecisionBypassed:
		return automations.BypassedSummary{}, nil
	case automations.ConditionDecisionEvaluated:
		result := automations.ConditionResult(row.ConditionResult.String)
		switch result {
		case automations.ConditionTrue, automations.ConditionFalse, automations.ConditionUnknown:
			return automations.EvaluatedSummary{Result: result}, nil
		default:
			return nil, lifecycleCorruption("unknown Condition summary result")
		}
	default:
		return nil, lifecycleCorruption("unknown Condition summary mode")
	}
}
