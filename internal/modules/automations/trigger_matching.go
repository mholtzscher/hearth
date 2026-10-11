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
// retained Skip can preserve the definition that matched. Input must be unchanged
// normalized definition data. Returned snapshots own their mutable slices and bytes
// and retain the prepared immutable cron clock fields.
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
		owned, err := clonePreparedTrigger(trigger)
		if err != nil {
			return nil, err
		}
		trigger = owned
		snapshots = append(snapshots, trigger)
	}
	return snapshots, nil
}

func clonePreparedTrigger(trigger Trigger) (Trigger, error) {
	switch body := trigger.Body.(type) {
	case ObservationTrigger:
		body.Dispositions = slices.Clone(body.Dispositions)
		body.PreviousComparisons = cloneObservationComparisons(body.PreviousComparisons)
		body.Comparisons = cloneObservationComparisons(body.Comparisons)
		trigger.Body = body
	case EntityEventTrigger:
		trigger.Body = body
	case HeldStateTrigger:
		body.Comparisons = cloneObservationComparisons(body.Comparisons)
		trigger.Body = body
	case CronTrigger:
		trigger.Body = body
	default:
		return Trigger{}, invalid("trigger %q: unsupported body", trigger.ID)
	}
	return trigger, nil
}

func matchAutomationTrigger(fact DeviceFact, trigger Trigger) (bool, error) {
	switch body := trigger.Body.(type) {
	case ObservationTrigger:
		switch fact := fact.(type) {
		case ObservationFact:
			return matchObservationTrigger(&fact, &body)
		case EntityEventFact:
			return false, nil
		default:
			return false, invalidFact("unsupported Fact payload")
		}
	case EntityEventTrigger:
		switch fact := fact.(type) {
		case EntityEventFact:
			return fact.EntityID == body.EntityID && fact.Name == body.EventName, nil
		case ObservationFact:
			return false, nil
		default:
			return false, invalidFact("unsupported Fact payload")
		}
	case HeldStateTrigger, CronTrigger:
		// Temporal Triggers are evaluated by their workers, never by immediate
		// Device Fact admission.
		return false, nil
	default:
		return false, fmt.Errorf("%w: trigger %q has unknown kind %q", ErrInvalidAutomation, trigger.ID, trigger.Kind())
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
