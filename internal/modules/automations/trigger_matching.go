package automations

import (
	"fmt"
	"slices"
)

// MatchTriggers returns the IDs of every Trigger in one definition the Fact
// matches, in definition order.
func MatchTriggers(fact DeviceFact, definition Definition) ([]TriggerID, error) {
	var matched []TriggerID
	for _, trigger := range definition.Triggers {
		matches, err := matchAutomationTrigger(fact, trigger)
		if err != nil {
			return nil, err
		}
		if matches {
			matched = append(matched, trigger.ID)
		}
	}
	return matched, nil
}

// MatchedTriggerSnapshots selects Triggers in the supplied match order so a
// retained Skip can preserve the definition that matched. Nested values remain
// shared with definition; callers must not mutate them before persistence encodes.
func MatchedTriggerSnapshots(
	definition Definition,
	matched []TriggerID,
) ([]Trigger, error) {
	byID := make(map[TriggerID]Trigger, len(definition.Triggers))
	for _, trigger := range definition.Triggers {
		byID[trigger.ID] = trigger
	}
	snapshots := make([]Trigger, 0, len(matched))
	for _, id := range matched {
		trigger, found := byID[id]
		if !found {
			return nil, fmt.Errorf("%w: matched trigger %q is not in the definition", ErrInvalidAutomation, id)
		}
		snapshots = append(snapshots, trigger)
	}
	return snapshots, nil
}

func matchAutomationTrigger(fact DeviceFact, trigger Trigger) (bool, error) {
	switch trigger.Kind {
	case TriggerKindObservation:
		if fact.Family != DeviceFactObservation || fact.Observation == nil || trigger.Observation == nil {
			return false, nil
		}
		return matchObservationTrigger(fact.Observation, trigger.Observation)
	case TriggerKindEntityEvent:
		if fact.Family != DeviceFactEntityEvent || fact.EntityEvent == nil || trigger.EntityEvent == nil {
			return false, nil
		}
		return fact.EntityEvent.EntityID == trigger.EntityEvent.EntityID &&
			fact.EntityEvent.Name == trigger.EntityEvent.EventName, nil
	case TriggerKindHeldState:
		// Held-state Triggers are evaluated by the deadline worker, never by
		// immediate Device Fact admission.
		return false, nil
	default:
		return false, fmt.Errorf("%w: trigger %q has unknown kind %q", ErrInvalidAutomation, trigger.ID, trigger.Kind)
	}
}

func matchObservationTrigger(fact *ObservationFact, trigger *ObservationTrigger) (bool, error) {
	if fact.EntityID != trigger.EntityID {
		return false, nil
	}
	if !slices.Contains(trigger.Dispositions, fact.Disposition) {
		return false, nil
	}
	if len(trigger.PreviousComparisons) > 0 && fact.PreviousValue == nil {
		return false, nil
	}
	for _, comparison := range trigger.PreviousComparisons {
		matches, err := MatchObservationComparison(comparison, fact.PreviousValue)
		if err != nil {
			return false, err
		}
		if !matches {
			return false, nil
		}
	}
	for _, comparison := range trigger.Comparisons {
		matches, err := MatchObservationComparison(comparison, fact.Value)
		if err != nil {
			return false, err
		}
		if !matches {
			return false, nil
		}
	}
	return true, nil
}
