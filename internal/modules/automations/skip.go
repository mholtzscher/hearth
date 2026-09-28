package automations

import (
	"time"
)

// SkipID is the durable identity of one recorded Skip (ask_ UUIDv7).
type SkipID string

// ValidateSkipConditionDecision checks one Skip's decision against its reason. A
// Skip never records a bypass; a condition Skip must have evaluated false or
// unknown to match its reason.
func ValidateSkipConditionDecision(skip Skip) error {
	decision := skip.ConditionDecision
	if decision.DecisionMode() == ConditionDecisionBypassed {
		return invalid("condition decision: a Skip is never a bypass")
	}
	switch skip.Reason {
	case SkipStaleFact, SkipBusy:
		if decision.DecisionMode() == ConditionDecisionEvaluated {
			return invalid("condition decision: a stale or busy Skip does not evaluate conditions")
		}
	case SkipConditionsFalse, SkipConditionsUnknown:
		if decision.DecisionMode() != ConditionDecisionEvaluated {
			return invalid("condition decision: a condition Skip requires an evaluated decision")
		}
		want := ConditionFalse
		if skip.Reason == SkipConditionsUnknown {
			want = ConditionUnknown
		}
		if decision.DecisionEvaluation().Result != want {
			return invalid("condition decision: a condition Skip result matches its reason")
		}
	default:
		return invalid("condition decision: unknown skip reason %q", skip.Reason)
	}
	return nil
}

// SkipReason identifies why a matching Fact did not start a Run.
type SkipReason string

const (
	// SkipBusy marks a matching Automation that already had a running Run.
	SkipBusy SkipReason = "automation_busy"
	// SkipStaleFact marks a matching Fact older than the freshness bound.
	SkipStaleFact SkipReason = "stale_fact"
	// SkipConditionsFalse marks a matching Automation whose Conditions
	// evaluated false.
	SkipConditionsFalse SkipReason = "conditions_false"
	// SkipConditionsUnknown marks a matching Automation whose Conditions
	// evaluated unknown.
	SkipConditionsUnknown SkipReason = "conditions_unknown"
)

// Skip is one recorded non-Run outcome with its admission provenance,
// immutable matching Trigger snapshots, and an admission Condition decision. A
// device-fact Skip carries complete Fact evidence; a held-state Skip carries
// hold evidence and one matched Trigger; a manual Skip carries neither.
type Skip struct {
	ID                SkipID
	AutomationID      AutomationID
	AutomationName    string
	Revision          int64
	Source            RunSource          // device_fact, manual, or held_state admission provenance
	Fact              *DeviceFactSummary // non-nil iff Source is RunSourceDeviceFact
	HeldState         *HeldStateEvidence // non-nil iff Source is RunSourceHeldState
	MatchedTriggers   []Trigger          // nonempty iff Source is RunSourceDeviceFact
	Reason            SkipReason
	ConditionDecision ConditionDecision
	SkippedAt         time.Time
}

// ValidateSkip rejects a Skip whose identity, provenance, evidence, reason,
// Condition decision, or timestamp is impossible.
func ValidateSkip(skip Skip) error {
	if _, err := ParseSkipID(string(skip.ID)); err != nil {
		return err
	}
	if _, err := ParseAutomationID(string(skip.AutomationID)); err != nil {
		return err
	}
	if skip.Revision < 1 {
		return invalid("skip %q: revision must be at least 1", skip.ID)
	}
	if err := validateSkipProvenance(skip); err != nil {
		return err
	}
	switch skip.Reason {
	case SkipBusy, SkipStaleFact, SkipConditionsFalse, SkipConditionsUnknown:
	default:
		return invalid("skip %q: unknown reason %q", skip.ID, skip.Reason)
	}
	if skip.SkippedAt.IsZero() {
		return invalid("skip %q: skip time is required", skip.ID)
	}
	return ValidateSkipConditionDecision(skip)
}

// validateSkipProvenance checks the Source-discriminated Skip family: complete
// Fact evidence with matched Triggers for a device-fact Skip, and neither for a
// manual Skip.
func validateSkipProvenance(skip Skip) error {
	switch skip.Source {
	case RunSourceDeviceFact:
		if err := validateDeviceFactSkipProvenance(skip); err != nil {
			return err
		}
	case RunSourceManual:
		if err := validateManualSkipProvenance(skip); err != nil {
			return err
		}
	case RunSourceHeldState:
		if err := validateHeldStateSkipProvenance(skip); err != nil {
			return err
		}
	default:
		return invalid("skip %q: unknown source %q", skip.ID, skip.Source)
	}
	seen := make(map[TriggerID]bool, len(skip.MatchedTriggers))
	for _, trigger := range skip.MatchedTriggers {
		if err := ValidateTrigger(trigger); err != nil {
			return err
		}
		if seen[trigger.ID] {
			return invalid("skip %q: matched trigger IDs must be unique", skip.ID)
		}
		seen[trigger.ID] = true
	}
	return nil
}

func validateDeviceFactSkipProvenance(skip Skip) error {
	if skip.Fact == nil || skip.HeldState != nil {
		return invalid("skip %q: device fact Skip requires Fact evidence", skip.ID)
	}
	if err := ValidateDeviceFactSummary(*skip.Fact); err != nil {
		return err
	}
	if len(skip.MatchedTriggers) == 0 {
		return invalid("skip %q: device fact Skip requires matched triggers", skip.ID)
	}
	return nil
}

func validateManualSkipProvenance(skip Skip) error {
	if skip.Fact != nil || skip.HeldState != nil {
		return invalid("skip %q: manual Skip carries admission evidence", skip.ID)
	}
	if len(skip.MatchedTriggers) != 0 {
		return invalid("skip %q: manual Skip carries matched triggers", skip.ID)
	}
	if skip.Reason != SkipConditionsFalse && skip.Reason != SkipConditionsUnknown {
		return invalid("skip %q: manual Skip reason must be a condition outcome", skip.ID)
	}
	return nil
}

func validateHeldStateSkipProvenance(skip Skip) error {
	if skip.Fact != nil || skip.HeldState == nil || len(skip.MatchedTriggers) != 1 {
		return invalid("skip %q: held-state Skip requires one matched Trigger and hold evidence, without Fact", skip.ID)
	}
	if err := validateHeldStateEvidence(*skip.HeldState); err != nil {
		return err
	}
	if skip.Reason == SkipStaleFact {
		return invalid("skip %q: held-state Skip cannot be stale_fact", skip.ID)
	}
	if skip.MatchedTriggers[0].ID != skip.HeldState.TriggerID || skip.MatchedTriggers[0].Kind != TriggerKindHeldState {
		return invalid("skip %q: held-state evidence does not match its Trigger", skip.ID)
	}
	return nil
}
