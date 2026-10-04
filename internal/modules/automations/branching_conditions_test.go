package automations //nolint:testpackage // Shared prepared evaluator is private; no test-only export.

import (
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// The oracle is the configured-order intersection specified by A4. This test
// owns the shared evaluator's new leaf, not branch selection or execution.
//
//nolint:gocognit // Asserts leaf evidence and root negation separately across the contract cases.
func TestPreparedTriggerConditionsUseImmutableMatchSet(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		configured []TriggerID
		matched    []TriggerID
		negated    bool
		result     ConditionResult
		evidence   []TriggerID
	}{
		{"intersection", []TriggerID{"b", "c"}, []TriggerID{"a", "b"}, false, ConditionTrue, []TriggerID{"b"}},
		{"no intersection", []TriggerID{"c"}, []TriggerID{"a", "b"}, false, ConditionFalse, []TriggerID{}},
		{"manual", []TriggerID{"b"}, nil, false, ConditionFalse, []TriggerID{}},
		{"negated manual", []TriggerID{"b"}, nil, true, ConditionTrue, []TriggerID{}},
		{"multiple scheduled order", []TriggerID{"c", "b", "a"}, []TriggerID{"a", "b", "c"}, false, ConditionTrue, []TriggerID{"c", "b", "a"}},
		{"held provenance", []TriggerID{"held"}, []TriggerID{"held"}, false, ConditionTrue, []TriggerID{"held"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := Condition{
				ID:      "match",
				Kind:    ConditionTrigger,
				Trigger: &TriggerCondition{TriggerIDs: tc.configured},
			}
			if tc.negated {
				child := root
				root = Condition{ID: "not", Kind: ConditionNot, Child: &child}
			}
			walk := stepTreePreparation{triggerIDs: map[TriggerID]bool{"a": true, "b": true, "c": true, "held": true}}
			prepared, err := walk.condition(root, true)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
			evaluation, err := evaluateCoveredConditions(prepared, devices.EntityStateSnapshot{}, at, tc.matched)
			if err != nil {
				t.Fatal(err)
			}
			if evaluation.Result != tc.result || len(evaluation.Nodes) != 1 || !evaluation.EvaluatedAt.Equal(at) {
				t.Fatalf("evaluation = %#v", evaluation)
			}
			leaf := evaluation.Nodes[0]
			if leaf.Trigger == nil || !reflect.DeepEqual(leaf.Trigger.MatchedTriggerIDs, tc.evidence) {
				t.Fatalf("evidence = %#v, want %v", leaf.Trigger, tc.evidence)
			}
			if leaf.UnknownReason != nil || leaf.SelectedValue != nil || leaf.ObservationID != nil ||
				leaf.ObservedAt != nil {
				t.Fatalf("trigger contains State evidence: %#v", leaf)
			}
			if tc.negated && leaf.Result != ConditionFalse {
				t.Fatalf("negation changed leaf evidence: %#v", leaf)
			}
		})
	}
}
