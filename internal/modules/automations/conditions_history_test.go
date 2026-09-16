package automations_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// conditionTreeAndEvaluation builds one group tree whose leaves produce results,
// returning both the normalized tree and its evaluation.
func conditionTreeAndEvaluation(
	t *testing.T,
	results ...automations.ConditionResult,
) (automations.Condition, automations.ConditionEvaluation) {
	t.Helper()
	children := make([]automations.Condition, 0, len(results))
	entries := make([]devices.EntityStateSnapshotEntry, 0, len(results))
	for index, want := range results {
		entity := conditionEntity(index + 1)
		children = append(children, conditionLeaf(
			"leaf-"+string(rune('a'+index)), entity, "", automations.ComparisonEqual, "true", nil,
		))
		switch want {
		case automations.ConditionTrue:
			entries = append(entries, conditionState(entity, "true", conditionTime()))
		case automations.ConditionFalse:
			entries = append(entries, conditionState(entity, "false", conditionTime()))
		case automations.ConditionUnknown:
			entries = append(entries, conditionMissingState(entity))
		default:
			t.Fatalf("unsupported leaf result %q", want)
		}
	}
	tree := conditionGroup(automations.ConditionAll, children...)
	return tree, mustEvaluateCondition(t, tree, conditionSnapshot(entries...), conditionTime())
}

// Every decision mode round-trips through the strict codec unchanged, including
// an evaluated tree.
func TestAutomationConditionDecisionRoundTrip(t *testing.T) {
	t.Parallel()
	trueTree, trueEvaluation := conditionTreeAndEvaluation(
		t,
		automations.ConditionTrue,
		automations.ConditionTrue,
	)
	unknownTree, unknownEvaluation := conditionTreeAndEvaluation(
		t,
		automations.ConditionTrue,
		automations.ConditionUnknown,
	)
	cases := []struct {
		name     string
		decision automations.ConditionDecision
	}{
		{"not configured", automations.ConditionDecision{
			Mode: automations.ConditionDecisionNotConfigured,
		}},
		{"not configured with bypass requested", automations.ConditionDecision{
			Mode: automations.ConditionDecisionNotConfigured, BypassRequested: true,
		}},
		{"not evaluated", automations.ConditionDecision{
			Mode: automations.ConditionDecisionNotEvaluated, Snapshot: &trueTree,
		}},
		{"bypassed", automations.ConditionDecision{
			Mode: automations.ConditionDecisionBypassed, BypassRequested: true, Snapshot: &unknownTree,
		}},
		{"evaluated true", automations.ConditionDecision{
			Mode: automations.ConditionDecisionEvaluated, Snapshot: &trueTree, Evaluation: &trueEvaluation,
		}},
		{
			"evaluated unknown",
			automations.ConditionDecision{
				Mode:       automations.ConditionDecisionEvaluated,
				Snapshot:   &unknownTree,
				Evaluation: &unknownEvaluation,
			},
		},
	}
	for _, testCase := range cases {
		raw, err := automations.EncodeConditionDecision(testCase.decision)
		if err != nil {
			t.Errorf("%s: encode: %v", testCase.name, err)
			continue
		}
		decoded, err := automations.DecodeConditionDecision(raw)
		if err != nil {
			t.Errorf("%s: decode: %v", testCase.name, err)
			continue
		}
		if !reflect.DeepEqual(decoded, testCase.decision) {
			t.Errorf("%s: decoded = %#v, want %#v", testCase.name, decoded, testCase.decision)
		}
	}
}

// A selected JSON null survives the codec as real evidence, distinct from a
// missing selection on a missing-Entity leaf.
func TestAutomationConditionDecisionSelectedNullIsEvidence(t *testing.T) {
	t.Parallel()
	entity := conditionEntity(1)
	nullTree := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "null", nil)
	nullEvaluation := mustEvaluateCondition(
		t, nullTree, conditionSnapshot(conditionState(entity, "null", conditionTime())), conditionTime(),
	)
	missingTree := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", nil)
	missingEvaluation := mustEvaluateCondition(
		t, missingTree, conditionSnapshot(conditionAbsent(entity)), conditionTime(),
	)
	nullDecision := automations.ConditionDecision{
		Mode: automations.ConditionDecisionEvaluated, Snapshot: &nullTree, Evaluation: &nullEvaluation,
	}
	missingDecision := automations.ConditionDecision{
		Mode: automations.ConditionDecisionEvaluated, Snapshot: &missingTree, Evaluation: &missingEvaluation,
	}
	raw, err := automations.EncodeConditionDecision(nullDecision)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := automations.DecodeConditionDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	if value := decoded.Evaluation.Nodes[0].SelectedValue; value == nil || string(value) != "null" {
		t.Fatalf("selected null = %q, want bytes null", value)
	}
	raw, err = automations.EncodeConditionDecision(missingDecision)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = automations.DecodeConditionDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	node := decoded.Evaluation.Nodes[0]
	if node.SelectedValue != nil || node.ObservationID != nil || node.ObservedAt != nil {
		t.Fatalf("missing Entity evidence = %#v, want no value or Observation evidence", node)
	}
}

// Malformed decision JSON, including an empty or whitespace-only payload, is a
// permanent invalid-input error. A well-formed decision whose snapshot or
// evidence is merely unusual is deliberately accepted: reads decode the
// retained decision and trust it rather than re-proving it.
func TestDecodeAutomationConditionDecisionRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"array", `[]`},
		{"unknown field", `{"mode":"not_configured","bypass_requested":false,"extra":1}`},
		{"trailing content", `{"mode":"not_configured","bypass_requested":false} {}`},
		{"mode null", `{"mode":null,"bypass_requested":false}`},
		{"bypass null", `{"mode":"not_configured","bypass_requested":null}`},
		{"snapshot is not an object", `{"mode":"not_evaluated","bypass_requested":false,"snapshot":[]}`},
	}
	for _, testCase := range cases {
		if _, err := automations.DecodeConditionDecision(json.RawMessage(testCase.raw)); !errors.Is(
			err, automations.ErrInvalidAutomation,
		) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
	}
}

// The strict snapshot decoder must still accept a selected empty pointer: the
// schema requires the pointer member, not a nonempty value.
func TestDecodeAutomationConditionDecisionAcceptsSelectedEmptyPointer(t *testing.T) {
	t.Parallel()
	raw := `{"mode":"not_evaluated","bypass_requested":false,"snapshot":` +
		`{"id":"root","kind":"all","children":[` +
		`{"id":"leaf","kind":"entity_state","entity_id":"` + string(conditionEntity(1)) +
		`","pointer":"","operator":"eq","operand":true}]}}`
	decision, err := automations.DecodeConditionDecision(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("selected empty pointer: %v", err)
	}
	leaf := decision.Snapshot.Children[0]
	if leaf.EntityState == nil || leaf.EntityState.Pointer != "" {
		t.Fatalf("decoded empty pointer = %#v", leaf.EntityState)
	}
}

// The strict decision decoder rejects explicit null placeholders for every
// optional pointer member except selected_value, where JSON null is real
// evidence. An absent member stays valid.
func TestDecodeAutomationConditionDecisionRejectsExplicitNullPlaceholders(t *testing.T) {
	t.Parallel()
	entity := string(conditionEntity(1))
	observation := string(conditionObservationID(conditionEntity(1)))
	evaluatedAt := conditionTime().Format(time.RFC3339Nano)
	leafSnapshot := `{"id":"leaf","kind":"entity_state","entity_id":"` + entity +
		`","pointer":"","operator":"eq","operand":true}`
	nullOperandSnapshot := `{"id":"leaf","kind":"entity_state","entity_id":"` + entity +
		`","pointer":"","operator":"eq","operand":null}`
	evaluatedDecision := func(snapshot, result, nodes string) string {
		return `{"mode":"evaluated","bypass_requested":false,"snapshot":` + snapshot +
			`,"evaluation":{"evaluated_at":"` + evaluatedAt + `","result":"` + result + `","nodes":` + nodes + `}}`
	}
	cases := []struct {
		name  string
		raw   string
		valid bool
	}{
		{
			name: "snapshot explicit null",
			raw:  `{"mode":"not_configured","bypass_requested":false,"snapshot":null}`,
		},
		{
			name: "evaluation explicit null",
			raw:  `{"mode":"evaluated","bypass_requested":false,"snapshot":` + leafSnapshot + `,"evaluation":null}`,
		},
		{
			name: "node unknown_reason explicit null",
			raw: evaluatedDecision(
				leafSnapshot, "unknown", `[{"id":"leaf","result":"unknown","unknown_reason":null}]`,
			),
		},
		{
			name: "node observation_id explicit null",
			raw: evaluatedDecision(
				leafSnapshot, "true",
				`[{"id":"leaf","result":"true","selected_value":true,"observation_id":null,`+
					`"observed_at":"`+evaluatedAt+`"}]`,
			),
		},
		{
			name: "node observed_at explicit null",
			raw: evaluatedDecision(
				leafSnapshot, "true",
				`[{"id":"leaf","result":"true","selected_value":true,"observation_id":"`+observation+`",`+
					`"observed_at":null}]`,
			),
		},
		{
			name: "selected_value explicit null is evidence",
			raw: evaluatedDecision(
				nullOperandSnapshot, "true",
				`[{"id":"leaf","result":"true","selected_value":null,"observation_id":"`+observation+`",`+
					`"observed_at":"`+evaluatedAt+`"}]`,
			),
			valid: true,
		},
		{
			name: "absent optional members remain valid",
			raw: evaluatedDecision(
				leafSnapshot, "unknown", `[{"id":"leaf","result":"unknown","unknown_reason":"entity_missing"}]`,
			),
			valid: true,
		},
	}
	for _, testCase := range cases {
		decision, err := automations.DecodeConditionDecision(json.RawMessage(testCase.raw))
		if testCase.valid {
			if err != nil {
				t.Errorf("%s: %v, want a valid decision", testCase.name, err)
				continue
			}
			if decision.Mode != automations.ConditionDecisionEvaluated {
				t.Errorf("%s: mode = %q, want evaluated", testCase.name, decision.Mode)
			}
			continue
		}
		if !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
	}
}

// Run decisions never record not_evaluated, bypass only a manual Run, and
// admit on a true root.
func TestValidateRunConditionDecision(t *testing.T) {
	t.Parallel()
	trueTree, trueEvaluation := conditionTreeAndEvaluation(
		t, automations.ConditionTrue, automations.ConditionTrue,
	)
	falseTree, falseEvaluation := conditionTreeAndEvaluation(
		t, automations.ConditionTrue, automations.ConditionFalse,
	)
	run := func(
		conditions *automations.Condition,
		source automations.RunSource,
		decision automations.ConditionDecision,
	) automations.Run {
		return automations.Run{
			Source:            source,
			Snapshot:          automations.Definition{Conditions: conditions},
			ConditionDecision: decision,
		}
	}
	valid := run(&trueTree, automations.RunSourceManual, automations.ConditionDecision{
		Mode: automations.ConditionDecisionEvaluated, Snapshot: &trueTree, Evaluation: &trueEvaluation,
	})
	if err := automations.ValidateRunConditionDecision(valid); err != nil {
		t.Fatalf("valid evaluated Run: %v", err)
	}
	bypassed := run(&trueTree, automations.RunSourceManual, automations.ConditionDecision{
		Mode: automations.ConditionDecisionBypassed, BypassRequested: true, Snapshot: &trueTree,
	})
	if err := automations.ValidateRunConditionDecision(bypassed); err != nil {
		t.Fatalf("valid bypassed Run: %v", err)
	}
	// A manual unconditioned Run may record a requested bypass; no automatic
	// outcome ever may.
	manualUnconditionedBypass := run(nil, automations.RunSourceManual, automations.ConditionDecision{
		Mode: automations.ConditionDecisionNotConfigured, BypassRequested: true,
	})
	if err := automations.ValidateRunConditionDecision(manualUnconditionedBypass); err != nil {
		t.Fatalf("valid manual unconditioned bypass Run: %v", err)
	}
	cases := []struct {
		name string
		run  automations.Run
	}{
		{"not evaluated", run(&trueTree, automations.RunSourceManual, automations.ConditionDecision{
			Mode: automations.ConditionDecisionNotEvaluated, Snapshot: &trueTree,
		})},
		{"automatic bypass", run(&trueTree, automations.RunSourceDeviceFact, automations.ConditionDecision{
			Mode: automations.ConditionDecisionBypassed, BypassRequested: true, Snapshot: &trueTree,
		})},
		{
			"automatic unconditioned bypass request",
			run(nil, automations.RunSourceDeviceFact, automations.ConditionDecision{
				Mode: automations.ConditionDecisionNotConfigured, BypassRequested: true,
			}),
		},
		{"evaluated false root", run(&falseTree, automations.RunSourceManual, automations.ConditionDecision{
			Mode: automations.ConditionDecisionEvaluated, Snapshot: &falseTree, Evaluation: &falseEvaluation,
		})},
	}
	for _, testCase := range cases {
		if err := automations.ValidateRunConditionDecision(testCase.run); !errors.Is(
			err, automations.ErrInvalidAutomation,
		) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
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
	validFalse := skip(automations.SkipConditionsFalse, automations.ConditionDecision{
		Mode: automations.ConditionDecisionEvaluated, Snapshot: &falseTree, Evaluation: &falseEvaluation,
	})
	if err := automations.ValidateSkipConditionDecision(validFalse); err != nil {
		t.Fatalf("valid conditions_false Skip: %v", err)
	}
	validUnknown := skip(automations.SkipConditionsUnknown, automations.ConditionDecision{
		Mode: automations.ConditionDecisionEvaluated, Snapshot: &unknownTree, Evaluation: &unknownEvaluation,
	})
	if err := automations.ValidateSkipConditionDecision(validUnknown); err != nil {
		t.Fatalf("valid conditions_unknown Skip: %v", err)
	}
	validStale := skip(automations.SkipStaleFact, automations.ConditionDecision{
		Mode: automations.ConditionDecisionNotEvaluated, Snapshot: &falseTree,
	})
	if err := automations.ValidateSkipConditionDecision(validStale); err != nil {
		t.Fatalf("valid stale Skip: %v", err)
	}
	cases := []struct {
		name string
		skip automations.Skip
	}{
		{
			"reason result mismatch",
			skip(automations.SkipConditionsFalse, automations.ConditionDecision{
				Mode:       automations.ConditionDecisionEvaluated,
				Snapshot:   &falseTree,
				Evaluation: &unknownEvaluation,
			}),
		},
		{
			"condition reason without evaluation",
			skip(automations.SkipConditionsFalse, automations.ConditionDecision{
				Mode: automations.ConditionDecisionNotEvaluated, Snapshot: &falseTree,
			}),
		},
		{
			"stale Skip with evaluation",
			skip(automations.SkipStaleFact, automations.ConditionDecision{
				Mode:       automations.ConditionDecisionEvaluated,
				Snapshot:   &falseTree,
				Evaluation: &falseEvaluation,
			}),
		},
		{"busy Skip with bypass", skip(automations.SkipBusy, automations.ConditionDecision{
			Mode: automations.ConditionDecisionBypassed, BypassRequested: true, Snapshot: &falseTree,
		})},
		{"unknown reason", skip(automations.SkipReason("mystery"), automations.ConditionDecision{
			Mode: automations.ConditionDecisionNotEvaluated, Snapshot: &falseTree,
		})},
	}
	for _, testCase := range cases {
		if err := automations.ValidateSkipConditionDecision(testCase.skip); !errors.Is(
			err, automations.ErrInvalidAutomation,
		) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
	}
}

// The retained snapshot makes a Skip explainable after the definition changed:
// a decision snapshot decodes independently of any current definition.
func TestAutomationConditionDecisionRetainsSnapshotAfterDefinitionChange(t *testing.T) {
	t.Parallel()
	tree, evaluation := conditionTreeAndEvaluation(t, automations.ConditionTrue)
	decision := automations.ConditionDecision{
		Mode: automations.ConditionDecisionEvaluated, Snapshot: &tree, Evaluation: &evaluation,
	}
	raw, err := automations.EncodeConditionDecision(decision)
	if err != nil {
		t.Fatal(err)
	}
	// The original tree is mutated after persisting, representing a definition
	// edit or deletion; the persisted decision must stay self-consistent.
	tree.Children[0].ID = "renamed"
	decoded, err := automations.DecodeConditionDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Snapshot == nil || decoded.Snapshot.Children[0].ID != "leaf-a" {
		t.Fatalf("retained snapshot = %#v", decoded.Snapshot)
	}
	if err = automations.ValidateConditionDecision(decoded); err != nil {
		t.Fatalf("retained decision no longer validates: %v", err)
	}
}

// Fuzzing the strict decision decoder must never panic, and every accepted
// decision must survive an encode/decode cycle.
func FuzzDecodeAutomationConditionDecision(fuzz *testing.F) {
	fuzz.Add(`{"mode":"not_configured","bypass_requested":false}`)
	fuzz.Add(`{"mode":"not_evaluated","bypass_requested":false,"snapshot":` +
		`{"id":"root","kind":"all","children":[` +
		`{"id":"leaf","kind":"entity_state","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-000000000001",` +
		`"pointer":"","operator":"eq","operand":true}]}}`)
	fuzz.Fuzz(func(t *testing.T, raw string) {
		decision, err := automations.DecodeConditionDecision(json.RawMessage(raw))
		if err != nil {
			return
		}
		encoded, err := automations.EncodeConditionDecision(decision)
		if err != nil {
			t.Fatalf("accepted decision failed to encode: %v", err)
		}
		redecoded, err := automations.DecodeConditionDecision(encoded)
		if err != nil {
			t.Fatalf("accepted decision failed to re-decode: %v", err)
		}
		if !reflect.DeepEqual(decision, redecoded) {
			t.Fatalf("decision changed across round trip: %#v vs %#v", decision, redecoded)
		}
	})
}

// The codec is deterministic: encoding the same decision twice yields identical
// bytes and never embeds a definition.
func TestEncodeAutomationConditionDecisionIsDeterministic(t *testing.T) {
	t.Parallel()
	tree, evaluation := conditionTreeAndEvaluation(t, automations.ConditionTrue)
	decision := automations.ConditionDecision{
		Mode: automations.ConditionDecisionEvaluated, Snapshot: &tree, Evaluation: &evaluation,
	}
	first, err := automations.EncodeConditionDecision(decision)
	if err != nil {
		t.Fatal(err)
	}
	second, err := automations.EncodeConditionDecision(decision)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("encoding is not deterministic:\n%s\n%s", first, second)
	}
	if strings.Contains(string(first), `"steps"`) || strings.Contains(string(first), `"triggers"`) {
		t.Fatalf("decision encoding embeds a definition: %s", first)
	}
	if observedAt := evaluation.Nodes[1].ObservedAt; observedAt == nil || !observedAt.Equal(conditionTime().UTC()) {
		t.Fatalf("observed time = %v, want UTC evaluation time", observedAt)
	}
}
