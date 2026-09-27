package automations

import (
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// TriggerID is an author-supplied subject-safe slug identifying one Trigger within its own definition.
type TriggerID string

// TriggerKind is the closed discriminated family of one Automation Trigger.
type TriggerKind string

const (
	// TriggerKindObservation matches one accepted Observation Fact.
	TriggerKindObservation TriggerKind = "observation"
	// TriggerKindEntityEvent matches one accepted Entity Event Fact.
	TriggerKindEntityEvent TriggerKind = "entity_event"
	// TriggerKindHeldState starts an Automation after State has matched for a duration.
	TriggerKindHeldState TriggerKind = "held_state"
)

// ComparisonOperator is the closed set of typed Observation comparisons.
type ComparisonOperator string

const (
	// ComparisonEqual requires equal JSON type and equal JSON value.
	ComparisonEqual ComparisonOperator = "eq"
	// ComparisonNotEqual requires equal JSON type and a different value.
	ComparisonNotEqual ComparisonOperator = "ne"
	// ComparisonLessThan requires two finite JSON numbers with left < right.
	ComparisonLessThan ComparisonOperator = "lt"
	// ComparisonLessThanOrEqual requires two finite JSON numbers with left <= right.
	ComparisonLessThanOrEqual ComparisonOperator = "lte"
	// ComparisonGreaterThan requires two finite JSON numbers with left > right.
	ComparisonGreaterThan ComparisonOperator = "gt"
	// ComparisonGreaterThanOrEqual requires two finite JSON numbers with left >= right.
	ComparisonGreaterThanOrEqual ComparisonOperator = "gte"
)

// ObservationComparison is one typed comparison against an Observation Fact
// value. Pointer is RFC 6901; the empty pointer selects the whole value, and
// Operand is exactly one normalized JSON value.
type ObservationComparison struct {
	Pointer  string
	Operator ComparisonOperator
	Operand  json.RawMessage
}

// ObservationTrigger matches an Observation Fact by Entity, disposition, and up
// to eight independent comparisons against each side of the transition.
type ObservationTrigger struct {
	EntityID            devices.EntityID
	Dispositions        []devices.ObservationDisposition
	PreviousComparisons []ObservationComparison
	Comparisons         []ObservationComparison
}

// EntityEventTrigger matches one Entity Event Fact by exact Entity ID and event name.
type EntityEventTrigger struct {
	EntityID  devices.EntityID
	EventName devices.EntityEventName
}

// HeldStateTrigger matches the current State value while it remains equal to
// every configured comparison for ForSeconds.
type HeldStateTrigger struct {
	EntityID    devices.EntityID
	Comparisons []ObservationComparison
	ForSeconds  int64
}

// Trigger is one identified typed Trigger; exactly one family payload is set
// matching Kind.
type Trigger struct {
	ID          TriggerID
	Kind        TriggerKind
	Observation *ObservationTrigger
	EntityEvent *EntityEventTrigger
	HeldState   *HeldStateTrigger
}

// EntityID reports the single Entity this Trigger constrains.
func (trigger Trigger) EntityID() devices.EntityID {
	switch trigger.Kind {
	case TriggerKindObservation:
		if trigger.Observation != nil {
			return trigger.Observation.EntityID
		}
	case TriggerKindEntityEvent:
		if trigger.EntityEvent != nil {
			return trigger.EntityEvent.EntityID
		}
	case TriggerKindHeldState:
		if trigger.HeldState != nil {
			return trigger.HeldState.EntityID
		}
	}
	return ""
}

// ValidateTrigger rejects an impossible Trigger identity, family payload, or typed fields.
func ValidateTrigger(trigger Trigger) error {
	if _, err := ParseTriggerID(string(trigger.ID)); err != nil {
		return err
	}
	switch trigger.Kind {
	case TriggerKindObservation:
		if trigger.Observation == nil || trigger.EntityEvent != nil || trigger.HeldState != nil {
			return invalid("trigger %q: observation family payload mismatch", trigger.ID)
		}
		return validateObservationTrigger(*trigger.Observation)
	case TriggerKindEntityEvent:
		if trigger.EntityEvent == nil || trigger.Observation != nil || trigger.HeldState != nil {
			return invalid("trigger %q: entity event family payload mismatch", trigger.ID)
		}
		return validateEntityEventTrigger(*trigger.EntityEvent)
	case TriggerKindHeldState:
		if trigger.HeldState == nil || trigger.Observation != nil || trigger.EntityEvent != nil {
			return invalid("trigger %q: held state family payload mismatch", trigger.ID)
		}
		return validateHeldStateTrigger(*trigger.HeldState)
	default:
		return invalid("trigger %q: unknown kind %q", trigger.ID, trigger.Kind)
	}
}
func validateObservationTrigger(trigger ObservationTrigger) error {
	if _, err := devices.ParseEntityID(string(trigger.EntityID)); err != nil {
		return fmt.Errorf("%w: observation trigger entity: %w", ErrInvalidAutomation, err)
	}
	if len(trigger.Dispositions) == 0 || len(trigger.Dispositions) > automationDispositionMaxCount {
		return fmt.Errorf(
			"%w: observation trigger needs 1 to %d dispositions",
			ErrInvalidAutomation, automationDispositionMaxCount,
		)
	}
	seen := make(map[devices.ObservationDisposition]bool, len(trigger.Dispositions))
	for _, disposition := range trigger.Dispositions {
		if !acceptedObservationDisposition(disposition) {
			return fmt.Errorf(
				"%w: observation trigger disposition %q is not applied or unchanged",
				ErrInvalidAutomation, disposition,
			)
		}
		if seen[disposition] {
			return fmt.Errorf("%w: observation trigger dispositions must be unique", ErrInvalidAutomation)
		}
		seen[disposition] = true
	}
	if len(trigger.Comparisons) > automationComparisonMaxCount {
		return fmt.Errorf(
			"%w: observation trigger has more than %d comparisons",
			ErrInvalidAutomation, automationComparisonMaxCount,
		)
	}
	if len(trigger.PreviousComparisons) > automationComparisonMaxCount {
		return fmt.Errorf(
			"%w: observation trigger has more than %d previous comparisons",
			ErrInvalidAutomation, automationComparisonMaxCount,
		)
	}
	for _, comparison := range trigger.PreviousComparisons {
		if err := ValidateObservationComparison(comparison); err != nil {
			return err
		}
	}
	for _, comparison := range trigger.Comparisons {
		if err := ValidateObservationComparison(comparison); err != nil {
			return err
		}
	}
	return nil
}

func validateEntityEventTrigger(trigger EntityEventTrigger) error {
	if _, err := devices.ParseEntityID(string(trigger.EntityID)); err != nil {
		return fmt.Errorf("%w: entity event trigger entity: %w", ErrInvalidAutomation, err)
	}
	if !subjectSlugPattern.MatchString(string(trigger.EventName)) {
		return fmt.Errorf("%w: entity event trigger name is not a subject-safe slug", ErrInvalidAutomation)
	}
	return nil
}

func validateHeldStateTrigger(trigger HeldStateTrigger) error {
	if _, err := devices.ParseEntityID(string(trigger.EntityID)); err != nil {
		return fmt.Errorf("%w: held state trigger entity: %w", ErrInvalidAutomation, err)
	}
	if len(trigger.Comparisons) < 1 || len(trigger.Comparisons) > automationComparisonMaxCount {
		return fmt.Errorf(
			"%w: held state trigger needs 1 to %d comparisons",
			ErrInvalidAutomation, automationComparisonMaxCount,
		)
	}
	if trigger.ForSeconds < 1 || trigger.ForSeconds > 2_592_000 {
		return fmt.Errorf("%w: held state duration must be between 1 and 2592000 seconds", ErrInvalidAutomation)
	}
	for _, comparison := range trigger.Comparisons {
		if err := ValidateObservationComparison(comparison); err != nil {
			return err
		}
	}
	return nil
}
