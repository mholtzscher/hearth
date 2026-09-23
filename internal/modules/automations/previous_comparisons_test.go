package automations_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestMatchObservationTransitionUsesPreviousAndCurrentValues(t *testing.T) {
	t.Parallel()
	entityID, err := devices.ParseEntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if err != nil {
		t.Fatal(err)
	}
	triggerID, err := automations.ParseTriggerID("threshold")
	if err != nil {
		t.Fatal(err)
	}
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	definition := automations.Definition{Triggers: []automations.Trigger{{
		ID: triggerID, Kind: automations.TriggerKindObservation,
		Observation: &automations.ObservationTrigger{
			EntityID: entityID, Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
			PreviousComparisons: []automations.ObservationComparison{{
				Pointer: "", Operator: automations.ComparisonLessThanOrEqual, Operand: json.RawMessage("25"),
			}},
			Comparisons: []automations.ObservationComparison{{
				Pointer: "", Operator: automations.ComparisonGreaterThan, Operand: json.RawMessage("25"),
			}},
		},
	}}}
	fact := automations.DeviceFact{Family: automations.DeviceFactObservation, Observation: &automations.ObservationFact{
		FactID:        factID,
		ObservationID: observationID,
		EntityID:      entityID, Disposition: devices.DispositionApplied,
		Value: devices.Value("26"), PreviousValue: devices.Value("25"), EmittedAt: time.Now(),
	}}
	matched, err := automations.MatchTriggers(fact, definition)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0] != triggerID {
		t.Fatalf("upward crossing matches = %v, want [%s]", matched, triggerID)
	}
	fact.Observation.PreviousValue = devices.Value("26")
	matched, err = automations.MatchTriggers(fact, definition)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 0 {
		t.Fatalf("repeated value matches = %v, want no match", matched)
	}
}

func TestPreviousComparisonDefinitionRoundTripAndOwnership(t *testing.T) {
	t.Parallel()
	definition := runtimeDefinition(t, 1)
	comparison := json.RawMessage(`{"x":1}`)
	definition.Triggers[0].Observation.PreviousComparisons = []automations.ObservationComparison{{
		Pointer: "/state", Operator: automations.ComparisonEqual, Operand: comparison,
	}}
	normalized, err := automations.NormalizeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	comparison[2] = 'y'
	if string(normalized.Triggers[0].Observation.PreviousComparisons[0].Operand) != `{"x":1}` {
		t.Fatalf(
			"normalized operand aliased input: %s",
			normalized.Triggers[0].Observation.PreviousComparisons[0].Operand,
		)
	}
	encoded, err := automations.EncodeDefinition(normalized)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := automations.DecodeDefinition(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.Triggers[0].Observation.PreviousComparisons[0].Pointer; got != "/state" {
		t.Fatalf("round-trip previous pointer = %q", got)
	}
}

func TestPreviousComparisonsHaveIndependentEightEntryLimit(t *testing.T) {
	t.Parallel()
	definition := runtimeDefinition(t, 1)
	comparison := automations.ObservationComparison{
		Pointer: "", Operator: automations.ComparisonEqual, Operand: json.RawMessage("1"),
	}
	definition.Triggers[0].Observation.PreviousComparisons = make([]automations.ObservationComparison, 9)
	for index := range definition.Triggers[0].Observation.PreviousComparisons {
		definition.Triggers[0].Observation.PreviousComparisons[index] = comparison
	}
	if _, err := automations.NormalizeDefinition(definition); err == nil {
		t.Fatal("ninth previous comparison was accepted")
	}
	definition.Triggers[0].Observation.PreviousComparisons = nil
	definition.Triggers[0].Observation.Comparisons = make([]automations.ObservationComparison, 9)
	for index := range definition.Triggers[0].Observation.Comparisons {
		definition.Triggers[0].Observation.Comparisons[index] = comparison
	}
	if _, err := automations.NormalizeDefinition(definition); err == nil {
		t.Fatal("ninth current comparison was accepted")
	}
}

func TestPreviousComparisonsRequirePredecessorAndTreatNullAsPresent(t *testing.T) {
	t.Parallel()
	entityID, _ := devices.ParseEntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	triggerID, _ := automations.ParseTriggerID("has-previous-null")
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	definition := automations.Definition{Triggers: []automations.Trigger{{
		ID: triggerID, Kind: automations.TriggerKindObservation,
		Observation: &automations.ObservationTrigger{
			EntityID: entityID, Dispositions: []devices.ObservationDisposition{devices.DispositionUnchanged},
			PreviousComparisons: []automations.ObservationComparison{{
				Pointer: "", Operator: automations.ComparisonEqual, Operand: json.RawMessage("null"),
			}},
		},
	}}}
	fact := automations.DeviceFact{Family: automations.DeviceFactObservation, Observation: &automations.ObservationFact{
		FactID:        factID,
		ObservationID: observationID,
		EntityID:      entityID, Disposition: devices.DispositionUnchanged,
		Value: devices.Value("null"), EmittedAt: time.Now(),
	}}
	matched, err := automations.MatchTriggers(fact, definition)
	if err != nil || len(matched) != 0 {
		t.Fatalf("absent predecessor matched=%v err=%v, want no match", matched, err)
	}
	fact.Observation.PreviousValue = devices.Value("null")
	matched, err = automations.MatchTriggers(fact, definition)
	if err != nil || len(matched) != 1 {
		t.Fatalf("JSON-null predecessor matched=%v err=%v, want one match", matched, err)
	}
}

type transitionMatchCase struct {
	name                string
	previous, current   string
	previousComparisons []automations.ObservationComparison
	comparisons         []automations.ObservationComparison
	disposition         devices.ObservationDisposition
	eligible            []devices.ObservationDisposition
	wantMatch           bool
}

func TestMatchObservationTransitionComparisonGroups(t *testing.T) {
	t.Parallel()
	entityID, err := devices.ParseEntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if err != nil {
		t.Fatal(err)
	}

	tests := []transitionMatchCase{
		{
			name:     "off to on crossing",
			previous: `{"active":false}`, current: `{"active":true}`,
			previousComparisons: []automations.ObservationComparison{{
				Pointer: "/active", Operator: automations.ComparisonEqual, Operand: json.RawMessage("false"),
			}},
			comparisons: []automations.ObservationComparison{{
				Pointer: "/active", Operator: automations.ComparisonEqual, Operand: json.RawMessage("true"),
			}},
			wantMatch: true,
		},
		{
			name:     "on to off crossing",
			previous: `{"active":true}`, current: `{"active":false}`,
			previousComparisons: []automations.ObservationComparison{{
				Pointer: "/active", Operator: automations.ComparisonEqual, Operand: json.RawMessage("true"),
			}},
			comparisons: []automations.ObservationComparison{{
				Pointer: "/active", Operator: automations.ComparisonEqual, Operand: json.RawMessage("false"),
			}},
			wantMatch: true,
		},
		{
			name:     "numeric upward crossing",
			previous: `{"level":24}`, current: `{"level":26}`,
			previousComparisons: []automations.ObservationComparison{{
				Pointer: "/level", Operator: automations.ComparisonLessThanOrEqual, Operand: json.RawMessage("25"),
			}},
			comparisons: []automations.ObservationComparison{{
				Pointer: "/level", Operator: automations.ComparisonGreaterThan, Operand: json.RawMessage("25"),
			}},
			wantMatch: true,
		},
		{
			name:     "numeric downward crossing",
			previous: `{"level":26}`, current: `{"level":24}`,
			previousComparisons: []automations.ObservationComparison{{
				Pointer: "/level", Operator: automations.ComparisonGreaterThanOrEqual, Operand: json.RawMessage("25"),
			}},
			comparisons: []automations.ObservationComparison{{
				Pointer: "/level", Operator: automations.ComparisonLessThan, Operand: json.RawMessage("25"),
			}},
			wantMatch: true,
		},
		{
			name:     "range entered",
			previous: `{"level":9}`, current: `{"level":15}`,
			previousComparisons: []automations.ObservationComparison{{
				Pointer: "/level", Operator: automations.ComparisonLessThan, Operand: json.RawMessage("10"),
			}},
			comparisons: []automations.ObservationComparison{
				{Pointer: "/level", Operator: automations.ComparisonGreaterThanOrEqual, Operand: json.RawMessage("10")},
				{Pointer: "/level", Operator: automations.ComparisonLessThanOrEqual, Operand: json.RawMessage("20")},
			},
			wantMatch: true,
		},
		{
			name:     "range upper bound excludes value",
			previous: `{"level":9}`, current: `{"level":21}`,
			previousComparisons: []automations.ObservationComparison{{
				Pointer: "/level", Operator: automations.ComparisonLessThan, Operand: json.RawMessage("10"),
			}},
			comparisons: []automations.ObservationComparison{
				{Pointer: "/level", Operator: automations.ComparisonGreaterThanOrEqual, Operand: json.RawMessage("10")},
				{Pointer: "/level", Operator: automations.ComparisonLessThanOrEqual, Operand: json.RawMessage("20")},
			},
		},
		{
			name:     "previous-only group",
			previous: `{"ready":true}`, current: `null`,
			previousComparisons: []automations.ObservationComparison{{
				Pointer: "/ready", Operator: automations.ComparisonEqual, Operand: json.RawMessage("true"),
			}},
			wantMatch: true,
		},
		{
			name:     "current-only group",
			previous: `null`, current: `{"ready":true}`,
			comparisons: []automations.ObservationComparison{{
				Pointer: "/ready", Operator: automations.ComparisonEqual, Operand: json.RawMessage("true"),
			}},
			wantMatch: true,
		},
		{
			name:     "both groups empty",
			previous: `null`, current: `null`,
			wantMatch: true,
		},
		{
			name:     "unchanged disposition remains eligible",
			previous: `{"ready":false}`, current: `{"ready":true}`,
			previousComparisons: []automations.ObservationComparison{{
				Pointer: "/ready", Operator: automations.ComparisonEqual, Operand: json.RawMessage("false"),
			}},
			comparisons: []automations.ObservationComparison{{
				Pointer: "/ready", Operator: automations.ComparisonEqual, Operand: json.RawMessage("true"),
			}},
			disposition: devices.DispositionUnchanged,
			eligible:    []devices.ObservationDisposition{devices.DispositionUnchanged},
			wantMatch:   true,
		},
		{
			name:    "absent previous evidence cannot match",
			current: `{"level":26}`,
			previousComparisons: []automations.ObservationComparison{{
				Pointer: "/level", Operator: automations.ComparisonLessThan, Operand: json.RawMessage("25"),
			}},
		},
		{
			name:     "missing pointer with ne does not match",
			previous: `{"level":24}`, current: `{"other":26}`,
			comparisons: []automations.ObservationComparison{{
				Pointer: "/level", Operator: automations.ComparisonNotEqual, Operand: json.RawMessage("25"),
			}},
		},
		{
			name:     "invalid runtime array index does not match",
			previous: `[24]`, current: `[26]`,
			comparisons: []automations.ObservationComparison{{
				Pointer: "/01", Operator: automations.ComparisonEqual, Operand: json.RawMessage("26"),
			}},
		},
		{
			name:     "incompatible current type with ne does not match",
			previous: `{"level":24}`, current: `{"level":"26"}`,
			comparisons: []automations.ObservationComparison{{
				Pointer: "/level", Operator: automations.ComparisonNotEqual, Operand: json.RawMessage("25"),
			}},
		},
		{
			name:     "incompatible previous type with ne does not match",
			previous: `{"level":"24"}`, current: `{"level":26}`,
			previousComparisons: []automations.ObservationComparison{{
				Pointer: "/level", Operator: automations.ComparisonNotEqual, Operand: json.RawMessage("25"),
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := matchTransitionCase(t, entityID, test); got != test.wantMatch {
				t.Fatalf("matched = %v, want match %v", got, test.wantMatch)
			}
		})
	}
}

func matchTransitionCase(
	t *testing.T,
	entityID devices.EntityID,
	test transitionMatchCase,
) bool {
	t.Helper()
	disposition := test.disposition
	if disposition == "" {
		disposition = devices.DispositionApplied
	}
	eligible := test.eligible
	if eligible == nil {
		eligible = []devices.ObservationDisposition{devices.DispositionApplied}
	}
	triggerID, err := automations.ParseTriggerID("transition")
	if err != nil {
		t.Fatal(err)
	}
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	observation := &automations.ObservationFact{
		FactID: factID, ObservationID: observationID, EntityID: entityID,
		Disposition: disposition, Value: devices.Value(test.current),
		EmittedAt: time.Now(),
	}
	if test.previous != "" {
		observation.PreviousValue = devices.Value(test.previous)
	}
	definition := automations.Definition{Triggers: []automations.Trigger{{
		ID: triggerID, Kind: automations.TriggerKindObservation,
		Observation: &automations.ObservationTrigger{
			EntityID: entityID, Dispositions: eligible,
			PreviousComparisons: test.previousComparisons, Comparisons: test.comparisons,
		},
	}}}
	matched, err := automations.MatchTriggers(automations.DeviceFact{
		Family: automations.DeviceFactObservation, Observation: observation,
	}, definition)
	if err != nil {
		t.Fatal(err)
	}
	return len(matched) == 1
}

func TestUnmatchedObservationDoesNotCreateReceiptOrHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	entityID := newEntityID(t)
	service, _ := newRuntimeService(t, newScriptedDevices(), runtimeTestDependencies())
	definition := runtimeDefinitionFor(t, entityID)
	definition.Triggers[0].Observation.PreviousComparisons = []automations.ObservationComparison{{
		Pointer: "/temperature", Operator: automations.ComparisonGreaterThan, Operand: json.RawMessage("30"),
	}}
	record := createRuntimeAutomation(t, service, definition)
	fact := newObservationFact(t, entityID, runtimeTestNow)

	outcome, err := service.ReceiveDeviceFact(ctx, fact)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.MatchedAutomations != 0 || outcome.StartedRuns != 0 || outcome.RecordedSkips != 0 {
		t.Fatalf("unmatched fact outcome = %#v, want no admission", outcome)
	}
	if history := listHistory(t, service, record.ID); len(history) != 0 {
		t.Fatalf("unmatched fact created history: %#v", history)
	}

	// The same Fact must remain admissible after a definition change makes it match;
	// an earlier unmatched receipt would classify this delivery as a duplicate.
	definition.Triggers[0].Observation.PreviousComparisons = nil
	if _, replaceErr := service.ReplaceAutomation(ctx, record.ID, record.Revision, definition); replaceErr != nil {
		t.Fatal(replaceErr)
	}
	outcome, err = service.ReceiveDeviceFact(ctx, fact)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.MatchedAutomations != 1 || outcome.StartedRuns != 1 || outcome.DuplicateOutcomes != 0 {
		t.Fatalf("same fact after becoming eligible = %#v, want a new admission", outcome)
	}
	waitForRuns(t, service)
	if history := listHistory(t, service, record.ID); len(history) != 1 {
		t.Fatalf("history after newly matching delivery = %#v, want one entry", history)
	}
}
