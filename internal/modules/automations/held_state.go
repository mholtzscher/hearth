package automations

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// HeldStateDuration converts a validated held-state duration without allowing
// an integer overflow in [time.Duration]'s nanosecond representation.
func HeldStateDuration(seconds int64) (time.Duration, error) {
	if seconds < 1 || seconds > 2_592_000 {
		return 0, invalid("held state duration must be between 1 and 2592000 seconds")
	}
	return time.Duration(seconds) * time.Second, nil
}

// MatchHeldState compares a retained State value with every predicate in one
// held-state Trigger. Missing/incompatible selected values are non-matches;
// malformed stored JSON remains a comparison error.
func MatchHeldState(trigger HeldStateTrigger, value devices.Value) (bool, error) {
	if err := validateHeldStateTrigger(trigger); err != nil {
		return false, err
	}
	for _, comparison := range trigger.Comparisons {
		matched, err := MatchObservationComparison(comparison, value)
		if err != nil || !matched {
			return false, err
		}
	}
	return true, nil
}
