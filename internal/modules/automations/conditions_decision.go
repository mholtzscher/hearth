package automations

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// DecideConditions evaluates one configured Condition tree against a covering
// snapshot and reports the evaluated decision plus the Skip reason it implies
// ("" admits).
func DecideConditions(
	conditions *Condition,
	snapshot devices.EntityStateSnapshot,
	at time.Time,
) (ConditionDecision, SkipReason, error) {
	evaluation, err := EvaluateConditions(*conditions, snapshot, at)
	if err != nil {
		return nil, "", err
	}
	decision := EvaluatedDecision(*conditions, evaluation)
	switch evaluation.Result {
	case ConditionTrue:
		return decision, "", nil
	case ConditionFalse:
		return decision, SkipConditionsFalse, nil
	case ConditionUnknown:
		return decision, SkipConditionsUnknown, nil
	default:
		return nil, "", invalid("condition evaluation has an unknown result")
	}
}
