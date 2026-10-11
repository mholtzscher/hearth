package automations

// ConditionDecisionSummary is the admission explanation available without
// decoding a retained snapshot or evaluation.
//
//sumtype:decl
type ConditionDecisionSummary interface{ isConditionDecisionSummary() }

// NotConfiguredSummary identifies an admission with no Conditions.
type NotConfiguredSummary struct{}

// NotEvaluatedSummary identifies configured Conditions skipped before evaluation.
type NotEvaluatedSummary struct{}

// BypassedSummary identifies explicit manual bypass of configured Conditions.
type BypassedSummary struct{}

// EvaluatedSummary records the composed admission result.
type EvaluatedSummary struct{ Result ConditionResult }

func (NotConfiguredSummary) isConditionDecisionSummary() {}
func (NotEvaluatedSummary) isConditionDecisionSummary()  {}
func (BypassedSummary) isConditionDecisionSummary()      {}
func (EvaluatedSummary) isConditionDecisionSummary()     {}

// ConditionSummaryLabels projects established summary evidence for SQL and JSON.
func ConditionSummaryLabels(summary ConditionDecisionSummary) (ConditionDecisionMode, bool, *ConditionResult) {
	switch value := summary.(type) {
	case NotConfiguredSummary:
		return ConditionDecisionNotConfigured, false, nil
	case NotEvaluatedSummary:
		return ConditionDecisionNotEvaluated, false, nil
	case BypassedSummary:
		return ConditionDecisionBypassed, true, nil
	case EvaluatedSummary:
		return ConditionDecisionEvaluated, false, &value.Result
	default:
		panic("invalid retained Condition summary")
	}
}
