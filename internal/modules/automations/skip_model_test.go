package automations_test

import (
	"errors"
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

// Skip decisions must match their reason and never record a bypass or evaluation
// for stale and busy outcomes.
func TestValidateSkipConditionDecision(t *testing.T) {
	t.Parallel()
	falseTree, falseEvaluation := conditionTreeAndEvaluation(
		t, automations.ConditionTrue, automations.ConditionFalse,
	)
	unknownTree, unknownEvaluation := conditionTreeAndEvaluation(
		t, automations.ConditionTrue, automations.ConditionUnknown,
	)
	skip := func(
		reason automations.SkipReason,
		decision automations.ConditionDecision,
	) automations.Skip {
		return automations.Skip{Reason: reason, ConditionDecision: decision}
	}
	validFalse := skip(automations.SkipConditionsFalse, automations.EvaluatedDecision(
		falseTree, falseEvaluation,
	))
	if err := automations.ValidateSkipConditionDecision(validFalse); err != nil {
		t.Fatalf("valid conditions_false Skip: %v", err)
	}
	validUnknown := skip(automations.SkipConditionsUnknown, automations.EvaluatedDecision(
		unknownTree, unknownEvaluation,
	))
	if err := automations.ValidateSkipConditionDecision(validUnknown); err != nil {
		t.Fatalf("valid conditions_unknown Skip: %v", err)
	}
	validStale := skip(automations.SkipStaleFact, automations.NotEvaluatedDecision(falseTree))
	if err := automations.ValidateSkipConditionDecision(validStale); err != nil {
		t.Fatalf("valid stale Skip: %v", err)
	}
	cases := []struct {
		name string
		skip automations.Skip
	}{
		{
			"reason result mismatch",
			skip(automations.SkipConditionsFalse, automations.EvaluatedDecision(
				falseTree, unknownEvaluation,
			)),
		},
		{
			"condition reason without evaluation",
			skip(automations.SkipConditionsFalse, automations.NotEvaluatedDecision(falseTree)),
		},
		{
			"stale Skip with evaluation",
			skip(automations.SkipStaleFact, automations.EvaluatedDecision(falseTree, falseEvaluation)),
		},
		{"busy Skip with bypass", skip(automations.SkipBusy, automations.BypassedDecision(falseTree))},
		{"unknown reason", skip(automations.SkipReason("mystery"), automations.NotEvaluatedDecision(falseTree))},
	}
	for _, testCase := range cases {
		if err := automations.ValidateSkipConditionDecision(testCase.skip); !errors.Is(
			err, automations.ErrInvalidAutomation,
		) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
	}
}
