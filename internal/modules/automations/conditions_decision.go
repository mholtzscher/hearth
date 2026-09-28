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
	required, err := RequiredConditionEntityIDs(*conditions)
	if err != nil {
		return nil, "", err
	}
	if missing := missingSnapshotCoverage(required, snapshot); len(missing) > 0 {
		return nil, "", &ConditionSnapshotRequiredError{RequiredEntityIDs: missing}
	}
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

// missingSnapshotCoverage returns the required set when the snapshot does not cover all of it.
func missingSnapshotCoverage(
	required []devices.EntityID,
	snapshot devices.EntityStateSnapshot,
) []devices.EntityID {
	for _, entityID := range required {
		if _, covered := snapshot.Entries[entityID]; !covered {
			return required
		}
	}
	return nil
}
