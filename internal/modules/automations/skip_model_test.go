package automations_test

import (
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func validDomainSkip(t *testing.T) automations.Skip {
	t.Helper()
	skipID, err := automations.NewSkipID()
	if err != nil {
		t.Fatal(err)
	}
	automationID, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	fact := newObservationFactSummary(t)
	return automations.Skip{
		ID:             skipID,
		AutomationID:   automationID,
		AutomationName: "Office light",
		Revision:       3,
		Source:         automations.RunSourceDeviceFact,
		Fact:           &fact,
		MatchedTriggers: []automations.Trigger{{
			ID:   "occupied_and_warm",
			Kind: automations.TriggerKindObservation,
			Observation: &automations.ObservationTrigger{
				EntityID:     newEntityID(t),
				Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
			},
		}},
		Reason:            automations.SkipBusy,
		ConditionDecision: automations.NotConfiguredDecision(),
		SkippedAt:         modelTestTime,
	}
}

// Retained Skips require Fact evidence, matched Trigger snapshots, a valid reason, and time.
func TestValidateAutomationSkipRejectsImpossibleCombinations(t *testing.T) {
	t.Parallel()
	if err := automations.ValidateSkip(validDomainSkip(t)); err != nil {
		t.Fatalf("valid skip rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(skip *automations.Skip)
	}{
		{"no matched triggers", func(skip *automations.Skip) { skip.MatchedTriggers = nil }},
		{"unknown reason", func(skip *automations.Skip) { skip.Reason = "later" }},
		{"zero skip time", func(skip *automations.Skip) { skip.SkippedAt = time.Time{} }},
		{"zero revision", func(skip *automations.Skip) { skip.Revision = 0 }},
		{"missing fact", func(skip *automations.Skip) { skip.Fact = nil }},
		{"manual source with fact", func(skip *automations.Skip) {
			skip.Source = automations.RunSourceManual
		}},
		{"unknown source", func(skip *automations.Skip) { skip.Source = "later" }},
		{"bypass decision", func(skip *automations.Skip) {
			skip.ConditionDecision = automations.BypassedDecision(automations.Condition{})
		}},
		{"condition reason without evaluation", func(skip *automations.Skip) {
			skip.Reason = automations.SkipConditionsFalse
		}},
		{"duplicate matched trigger", func(skip *automations.Skip) {
			skip.MatchedTriggers = append(skip.MatchedTriggers, skip.MatchedTriggers[0])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			skip := validDomainSkip(t)
			test.mutate(&skip)
			if err := automations.ValidateSkip(skip); err == nil {
				t.Fatal("impossible skip was accepted")
			}
		})
	}
}
