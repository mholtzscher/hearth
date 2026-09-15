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
	results ...automations.AutomationConditionResult,
) (automations.AutomationCondition, automations.AutomationConditionEvaluation) {
	t.Helper()
	children := make([]automations.AutomationCondition, 0, len(results))
	entries := make([]devices.EntityStateSnapshotEntry, 0, len(results))
	for index, want := range results {
		entity := conditionEntity(index + 1)
		children = append(children, conditionLeaf(
			"leaf-"+string(rune('a'+index)), entity, "", automations.ComparisonEqual, "true", nil,
		))
		switch want {
		case automations.AutomationConditionTrue:
			entries = append(entries, conditionState(entity, "true", conditionTime()))
		case automations.AutomationConditionFalse:
			entries = append(entries, conditionState(entity, "false", conditionTime()))
		case automations.AutomationConditionUnknown:
			entries = append(entries, conditionMissingState(entity))
		default:
			t.Fatalf("unsupported leaf result %q", want)
		}
	}
	tree := conditionGroup(automations.AutomationConditionAll, children...)
	return tree, mustEvaluateCondition(t, tree, conditionSnapshot(entries...), conditionTime())
}

func cloneConditionEvaluation(
	evaluation automations.AutomationConditionEvaluation,
) automations.AutomationConditionEvaluation {
	cloned := evaluation
	cloned.Nodes = append([]automations.AutomationConditionNodeResult(nil), evaluation.Nodes...)
	return cloned
}

// Every decision mode round-trips through the strict codec unchanged, including
// an evaluated tree.
func TestAutomationConditionDecisionRoundTrip(t *testing.T) {
	t.Parallel()
	trueTree, trueEvaluation := conditionTreeAndEvaluation(
		t,
		automations.AutomationConditionTrue,
		automations.AutomationConditionTrue,
	)
	unknownTree, unknownEvaluation := conditionTreeAndEvaluation(
		t,
		automations.AutomationConditionTrue,
		automations.AutomationConditionUnknown,
	)
	cases := []struct {
		name     string
		decision automations.AutomationConditionDecision
	}{
		{"not configured", automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionNotConfigured,
		}},
		{"not configured with bypass requested", automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionNotConfigured, BypassRequested: true,
		}},
		{"not evaluated", automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionNotEvaluated, Snapshot: &trueTree,
		}},
		{"bypassed", automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionBypassed, BypassRequested: true, Snapshot: &unknownTree,
		}},
		{"evaluated true", automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionEvaluated, Snapshot: &trueTree, Evaluation: &trueEvaluation,
		}},
		{
			"evaluated unknown",
			automations.AutomationConditionDecision{
				Mode:       automations.AutomationConditionDecisionEvaluated,
				Snapshot:   &unknownTree,
				Evaluation: &unknownEvaluation,
			},
		},
	}
	for _, testCase := range cases {
		raw, err := automations.EncodeAutomationConditionDecision(testCase.decision)
		if err != nil {
			t.Errorf("%s: encode: %v", testCase.name, err)
			continue
		}
		decoded, err := automations.DecodeAutomationConditionDecision(raw)
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
	nullDecision := automations.AutomationConditionDecision{
		Mode: automations.AutomationConditionDecisionEvaluated, Snapshot: &nullTree, Evaluation: &nullEvaluation,
	}
	missingDecision := automations.AutomationConditionDecision{
		Mode: automations.AutomationConditionDecisionEvaluated, Snapshot: &missingTree, Evaluation: &missingEvaluation,
	}
	raw, err := automations.EncodeAutomationConditionDecision(nullDecision)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := automations.DecodeAutomationConditionDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	if value := decoded.Evaluation.Nodes[0].SelectedValue; value == nil || string(value) != "null" {
		t.Fatalf("selected null = %q, want bytes null", value)
	}
	raw, err = automations.EncodeAutomationConditionDecision(missingDecision)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = automations.DecodeAutomationConditionDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	node := decoded.Evaluation.Nodes[0]
	if node.SelectedValue != nil || node.ObservationID != nil || node.ObservedAt != nil {
		t.Fatalf("missing Entity evidence = %#v, want no value or Observation evidence", node)
	}
}

// A legacy SQL NULL decision column normalizes to the explicit not_configured
// mode rather than a zero-valued mode.
func TestDecodeAutomationConditionDecisionNormalizesLegacyNull(t *testing.T) {
	t.Parallel()
	for _, raw := range []json.RawMessage{nil, []byte("   ")} {
		decision, err := automations.DecodeAutomationConditionDecision(raw)
		if err != nil {
			t.Fatal(err)
		}
		want := automations.AutomationConditionDecision{Mode: automations.AutomationConditionDecisionNotConfigured}
		if !reflect.DeepEqual(decision, want) {
			t.Fatalf("legacy decision = %#v, want %#v", decision, want)
		}
		encoded, err := automations.EncodeAutomationConditionDecision(decision)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(encoded, []byte(`"not_configured"`)) {
			t.Fatalf("legacy encode = %s, want explicit not_configured", encoded)
		}
	}
}

// Malformed or inconsistent decision JSON is a permanent invalid-input error.
func TestDecodeAutomationConditionDecisionRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	validSnapshot := `{"id":"root","kind":"all","children":[` +
		`{"id":"leaf","kind":"entity_state","entity_id":"` + string(conditionEntity(1)) +
		`","pointer":"","operator":"eq","operand":true}]}`
	cases := []struct {
		name string
		raw  string
	}{
		{"array", `[]`},
		{"unknown field", `{"mode":"not_configured","bypass_requested":false,"extra":1}`},
		{"trailing content", `{"mode":"not_configured","bypass_requested":false} {}`},
		{"mode null", `{"mode":null,"bypass_requested":false}`},
		{"bypass null", `{"mode":"not_configured","bypass_requested":null}`},
		{"unknown mode", `{"mode":"maybe","bypass_requested":false}`},
		{"empty mode", `{"mode":"","bypass_requested":false}`},
		{"not configured with snapshot", `{"mode":"not_configured","bypass_requested":false,"snapshot":` +
			validSnapshot + `}`},
		{"evaluated without evaluation", `{"mode":"evaluated","bypass_requested":false,"snapshot":` +
			validSnapshot + `}`},
		{"snapshot omits pointer", `{"mode":"not_evaluated","bypass_requested":false,"snapshot":` +
			`{"id":"leaf","kind":"entity_state","entity_id":"` + string(conditionEntity(1)) +
			`","operator":"eq","operand":true}}`},
		{"snapshot null pointer", `{"mode":"not_evaluated","bypass_requested":false,"snapshot":` +
			`{"id":"leaf","kind":"entity_state","entity_id":"` + string(conditionEntity(1)) +
			`","pointer":null,"operator":"eq","operand":true}}`},
		{"snapshot entity_state with children", `{"mode":"not_evaluated","bypass_requested":false,"snapshot":` +
			`{"id":"leaf","kind":"entity_state","entity_id":"` + string(conditionEntity(1)) +
			`","pointer":"","operator":"eq","operand":true,"children":[` +
			`{"id":"inner","kind":"entity_state","entity_id":"` + string(conditionEntity(1)) +
			`","pointer":"","operator":"eq","operand":true}]}}`},
		{"snapshot all with entity_id", `{"mode":"not_evaluated","bypass_requested":false,"snapshot":` +
			`{"id":"root","kind":"all","entity_id":"` + string(conditionEntity(1)) + `","children":[` +
			`{"id":"leaf","kind":"entity_state","entity_id":"` + string(conditionEntity(1)) +
			`","pointer":"","operator":"eq","operand":true}]}}`},
		{"snapshot not with children", `{"mode":"not_evaluated","bypass_requested":false,"snapshot":` +
			`{"id":"root","kind":"not","children":[` +
			`{"id":"leaf","kind":"entity_state","entity_id":"` + string(conditionEntity(1)) +
			`","pointer":"","operator":"eq","operand":true}]}}`},
		{"snapshot all omits children", `{"mode":"not_evaluated","bypass_requested":false,"snapshot":` +
			`{"id":"root","kind":"all"}}`},
		{"snapshot not omits child", `{"mode":"not_evaluated","bypass_requested":false,"snapshot":` +
			`{"id":"root","kind":"not"}}`},
		{"snapshot is not an object", `{"mode":"not_evaluated","bypass_requested":false,"snapshot":[]}`},
		{"bypassed without bypass request", `{"mode":"bypassed","bypass_requested":false,"snapshot":` +
			validSnapshot + `}`},
		{
			"snapshot with duplicate IDs",
			`{"mode":"not_evaluated","bypass_requested":false,"snapshot":{"id":"root","kind":"all","children":[` +
				`{"id":"same","kind":"entity_state","entity_id":"` + string(conditionEntity(1)) +
				`","pointer":"","operator":"eq","operand":true},` +
				`{"id":"same","kind":"entity_state","entity_id":"` + string(conditionEntity(2)) +
				`","pointer":"","operator":"eq","operand":true}]}}`,
		},
	}
	for _, testCase := range cases {
		if _, err := automations.DecodeAutomationConditionDecision(json.RawMessage(testCase.raw)); !errors.Is(
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
	decision, err := automations.DecodeAutomationConditionDecision(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("selected empty pointer: %v", err)
	}
	leaf := decision.Snapshot.Children[0]
	if leaf.EntityState == nil || leaf.EntityState.Pointer != "" {
		t.Fatalf("decoded empty pointer = %#v", leaf.EntityState)
	}
}

// The domain validator rejects fabricated or self-inconsistent evaluation
// evidence that a JSON-only decoder would accept.
func TestValidateAutomationConditionDecisionRejectsInconsistentEvidence(t *testing.T) {
	t.Parallel()
	tree, evaluation := conditionTreeAndEvaluation(
		t, automations.AutomationConditionTrue, automations.AutomationConditionUnknown,
	)
	base := func() automations.AutomationConditionDecision {
		return automations.AutomationConditionDecision{
			Mode:       automations.AutomationConditionDecisionEvaluated,
			Snapshot:   &tree,
			Evaluation: cloneEvaluationPointer(evaluation),
		}
	}
	trueLeaf := func(decision *automations.AutomationConditionDecision) *automations.AutomationConditionNodeResult {
		return &decision.Evaluation.Nodes[1]
	}
	unknownLeaf := func(decision *automations.AutomationConditionDecision) *automations.AutomationConditionNodeResult {
		return &decision.Evaluation.Nodes[2]
	}
	cases := []struct {
		name   string
		mutate func(*automations.AutomationConditionDecision)
	}{
		{"node ID mismatch", func(d *automations.AutomationConditionDecision) { d.Evaluation.Nodes[1].ID = "wrong" }},
		{"missing node", func(d *automations.AutomationConditionDecision) {
			d.Evaluation.Nodes = d.Evaluation.Nodes[:len(d.Evaluation.Nodes)-1]
		}},
		{"extra node", func(d *automations.AutomationConditionDecision) {
			d.Evaluation.Nodes = append(d.Evaluation.Nodes, automations.AutomationConditionNodeResult{ID: "extra"})
		}},
		{"root result mismatch", func(d *automations.AutomationConditionDecision) {
			d.Evaluation.Nodes[0].Result = automations.AutomationConditionTrue
		}},
		{"composite carries reason", func(d *automations.AutomationConditionDecision) {
			reason := automations.AutomationConditionUnknownStateMissing
			d.Evaluation.Nodes[0].UnknownReason = &reason
		}},
		{"composite carries selected value", func(d *automations.AutomationConditionDecision) {
			d.Evaluation.Nodes[0].SelectedValue = json.RawMessage("true")
		}},
		{"true leaf without selected value", func(d *automations.AutomationConditionDecision) {
			trueLeaf(d).SelectedValue = nil
		}},
		{"true leaf with unknown reason", func(d *automations.AutomationConditionDecision) {
			reason := automations.AutomationConditionUnknownStateMissing
			trueLeaf(d).UnknownReason = &reason
		}},
		{"unknown leaf without reason", func(d *automations.AutomationConditionDecision) {
			unknownLeaf(d).UnknownReason = nil
		}},
		{"unknown leaf with unlisted reason", func(d *automations.AutomationConditionDecision) {
			reason := automations.AutomationConditionUnknownReason("mystery")
			unknownLeaf(d).UnknownReason = &reason
		}},
		{"state_missing leaf carries Observation metadata", func(d *automations.AutomationConditionDecision) {
			observationID := devices.ObservationID("obs_01890f47-7a6b-7c4d-8e9f-000000000001")
			observedAt := conditionTime()
			unknownLeaf(d).ObservationID = &observationID
			unknownLeaf(d).ObservedAt = &observedAt
		}},
		{"type_mismatch leaf without Observation metadata", func(d *automations.AutomationConditionDecision) {
			reason := automations.AutomationConditionUnknownTypeMismatch
			unknownLeaf(d).UnknownReason = &reason
		}},
		{"observation ID without time", func(d *automations.AutomationConditionDecision) {
			trueLeaf(d).ObservedAt = nil
		}},
		{"node result empty", func(d *automations.AutomationConditionDecision) {
			trueLeaf(d).Result = ""
		}},
	}
	for _, testCase := range cases {
		decision := base()
		testCase.mutate(&decision)
		if err := automations.ValidateAutomationConditionDecision(decision); !errors.Is(
			err, automations.ErrInvalidAutomation,
		) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
		if _, err := automations.EncodeAutomationConditionDecision(decision); !errors.Is(
			err, automations.ErrInvalidAutomation,
		) {
			t.Errorf("%s: encode error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
	}
}

// The domain validator must reject impossible freshness evidence: a bounded
// reason without a max_age_seconds leaf, or a timestamp that is not actually
// future or past the bound at the evaluation time. The exact bound stays allowed,
// and the selected value stays optional when the pointer failed after the
// freshness reason took precedence.
func TestValidateConditionEvaluationRejectsImpossibleEvidence(t *testing.T) {
	t.Parallel()
	entity := conditionEntity(1)
	now := conditionTime()
	canonical := conditionObservationID(entity)
	bounded := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", conditionAge(300))
	unbounded := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", nil)
	at := func(delta time.Duration) *time.Time {
		value := now.Add(delta)
		return &value
	}
	unknown := func(
		reason automations.AutomationConditionUnknownReason,
		selected json.RawMessage,
		observationID devices.ObservationID,
		observedAt *time.Time,
	) automations.AutomationConditionNodeResult {
		return automations.AutomationConditionNodeResult{
			ID: "leaf", Result: automations.AutomationConditionUnknown, UnknownReason: &reason,
			SelectedValue: selected, ObservationID: &observationID, ObservedAt: observedAt,
		}
	}
	trueLeaf := automations.AutomationConditionNodeResult{
		ID: "leaf", Result: automations.AutomationConditionTrue, SelectedValue: json.RawMessage("true"),
		ObservationID: &canonical, ObservedAt: at(-300 * time.Second),
	}
	badObservation := devices.ObservationID("obs_not-canonical")
	cases := []struct {
		name  string
		leaf  automations.AutomationCondition
		node  automations.AutomationConditionNodeResult
		valid bool
	}{
		{
			name: "type_mismatch with value", leaf: bounded,
			node: unknown(
				automations.AutomationConditionUnknownTypeMismatch, json.RawMessage(`"true"`), canonical,
				at(-time.Second),
			),
			valid: true,
		},
		{
			name: "type_mismatch without value", leaf: bounded,
			node:  unknown(automations.AutomationConditionUnknownTypeMismatch, nil, canonical, at(-time.Second)),
			valid: false,
		},
		{
			name: "future evidence", leaf: bounded,
			node: unknown(
				automations.AutomationConditionUnknownEvidenceInFuture, json.RawMessage("true"), canonical,
				at(time.Second),
			),
			valid: true,
		},
		{
			name: "future evidence without a selected value", leaf: bounded,
			node:  unknown(automations.AutomationConditionUnknownEvidenceInFuture, nil, canonical, at(time.Second)),
			valid: true,
		},
		{
			name: "future evidence at the evaluation instant", leaf: bounded,
			node:  unknown(automations.AutomationConditionUnknownEvidenceInFuture, nil, canonical, at(0)),
			valid: false,
		},
		{
			name: "future evidence without an age bound", leaf: unbounded,
			node:  unknown(automations.AutomationConditionUnknownEvidenceInFuture, nil, canonical, at(time.Second)),
			valid: false,
		},
		{
			name: "expired evidence", leaf: bounded,
			node: unknown(
				automations.AutomationConditionUnknownEvidenceExpired, json.RawMessage("true"), canonical,
				at(-301*time.Second),
			),
			valid: true,
		},
		{
			name: "expired evidence without a selected value", leaf: bounded,
			node:  unknown(automations.AutomationConditionUnknownEvidenceExpired, nil, canonical, at(-301*time.Second)),
			valid: true,
		},
		{
			name: "expired evidence at the exact bound", leaf: bounded,
			node:  unknown(automations.AutomationConditionUnknownEvidenceExpired, nil, canonical, at(-300*time.Second)),
			valid: false,
		},
		{
			name: "expired evidence inside the bound", leaf: bounded,
			node:  unknown(automations.AutomationConditionUnknownEvidenceExpired, nil, canonical, at(-299*time.Second)),
			valid: false,
		},
		{
			name: "expired evidence without an age bound", leaf: unbounded,
			node:  unknown(automations.AutomationConditionUnknownEvidenceExpired, nil, canonical, at(-301*time.Second)),
			valid: false,
		},
		{
			name: "expired evidence in the future", leaf: bounded,
			node:  unknown(automations.AutomationConditionUnknownEvidenceExpired, nil, canonical, at(time.Second)),
			valid: false,
		},
		{
			name: "non-canonical Observation ID", leaf: bounded,
			node: unknown(
				automations.AutomationConditionUnknownTypeMismatch, json.RawMessage("true"), badObservation,
				at(-time.Second),
			),
			valid: false,
		},
		{
			name: "true leaf at the exact bound", leaf: bounded, node: trueLeaf, valid: true,
		},
	}
	for _, testCase := range cases {
		decision := automations.AutomationConditionDecision{
			Mode:     automations.AutomationConditionDecisionEvaluated,
			Snapshot: &testCase.leaf,
			Evaluation: &automations.AutomationConditionEvaluation{
				EvaluatedAt: now,
				Result:      testCase.node.Result,
				Nodes:       []automations.AutomationConditionNodeResult{testCase.node},
			},
		}
		err := automations.ValidateAutomationConditionDecision(decision)
		if testCase.valid && err != nil {
			t.Errorf("%s: %v, want a valid decision", testCase.name, err)
		}
		if !testCase.valid && !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
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
		decision, err := automations.DecodeAutomationConditionDecision(json.RawMessage(testCase.raw))
		if testCase.valid {
			if err != nil {
				t.Errorf("%s: %v, want a valid decision", testCase.name, err)
				continue
			}
			if decision.Mode != automations.AutomationConditionDecisionEvaluated {
				t.Errorf("%s: mode = %q, want evaluated", testCase.name, decision.Mode)
			}
			continue
		}
		if !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
	}
}

// The domain validator re-derives every definite leaf result and every
// type_mismatch verdict from the retained selection and the snapshot, so a
// fabricated result cannot pass, and it enforces the freshness precedence that
// outranks pointer and type failures.
func TestValidateConditionEvaluationRejectsFabricatedLeafEvidence(t *testing.T) {
	t.Parallel()
	entity := conditionEntity(1)
	now := conditionTime()
	canonical := conditionObservationID(entity)
	at := func(delta time.Duration) *time.Time {
		value := now.Add(delta)
		return &value
	}
	leaf := func(
		operator automations.ComparisonOperator,
		operand string,
		maxAge *int64,
	) automations.AutomationCondition {
		return conditionLeaf("leaf", entity, "", operator, operand, maxAge)
	}
	node := func(
		result automations.AutomationConditionResult,
		reason automations.AutomationConditionUnknownReason,
		selected string,
		observedAt *time.Time,
	) automations.AutomationConditionNodeResult {
		decoded := automations.AutomationConditionNodeResult{
			ID:            "leaf",
			Result:        result,
			ObservationID: &canonical,
			ObservedAt:    observedAt,
		}
		if reason != "" {
			reasonCopy := reason
			decoded.UnknownReason = &reasonCopy
		}
		if selected != "" {
			decoded.SelectedValue = json.RawMessage(selected)
		}
		return decoded
	}

	eqTrue := func(maxAge *int64) automations.AutomationCondition {
		return leaf(automations.ComparisonEqual, "true", maxAge)
	}
	cases := []struct {
		name  string
		leaf  automations.AutomationCondition
		node  automations.AutomationConditionNodeResult
		valid bool
	}{
		{
			name: "definite result equals exact comparison", leaf: eqTrue(nil),
			node:  node(automations.AutomationConditionTrue, "", `true`, at(-time.Second)),
			valid: true,
		},
		{
			name: "definite result contradicts exact comparison", leaf: eqTrue(nil),
			node:  node(automations.AutomationConditionFalse, "", `true`, at(-time.Second)),
			valid: false,
		},
		{
			name: "definite result with incompatible selection", leaf: eqTrue(nil),
			node:  node(automations.AutomationConditionTrue, "", `"true"`, at(-time.Second)),
			valid: false,
		},
		{
			name: "definite ne equal selection is false", leaf: leaf(automations.ComparisonNotEqual, "true", nil),
			node:  node(automations.AutomationConditionFalse, "", `true`, at(-time.Second)),
			valid: true,
		},
		{
			name: "definite ne equal selection recorded true", leaf: leaf(automations.ComparisonNotEqual, "true", nil),
			node:  node(automations.AutomationConditionTrue, "", `true`, at(-time.Second)),
			valid: false,
		},
		{
			name: "definite ordering comparison", leaf: leaf(automations.ComparisonLessThan, "30", nil),
			node:  node(automations.AutomationConditionTrue, "", `20`, at(-time.Second)),
			valid: true,
		},
		{
			name: "definite ordering comparison contradicts", leaf: leaf(automations.ComparisonLessThan, "30", nil),
			node:  node(automations.AutomationConditionFalse, "", `20`, at(-time.Second)),
			valid: false,
		},
		{
			name: "definite bounded future evidence", leaf: eqTrue(conditionAge(300)),
			node:  node(automations.AutomationConditionTrue, "", `true`, at(time.Second)),
			valid: false,
		},
		{
			name: "definite bounded expired evidence", leaf: eqTrue(conditionAge(300)),
			node:  node(automations.AutomationConditionTrue, "", `true`, at(-301*time.Second)),
			valid: false,
		},
		{
			name: "definite unbounded future evidence compares normally", leaf: eqTrue(nil),
			node:  node(automations.AutomationConditionTrue, "", `true`, at(time.Second)),
			valid: true,
		},
		{
			name: "type_mismatch with compatible selection", leaf: eqTrue(nil),
			node: node(
				automations.AutomationConditionUnknown, automations.AutomationConditionUnknownTypeMismatch,
				`true`, at(-time.Second),
			),
			valid: false,
		},
		{
			name: "type_mismatch with incompatible selection", leaf: eqTrue(nil),
			node: node(
				automations.AutomationConditionUnknown, automations.AutomationConditionUnknownTypeMismatch,
				`"true"`, at(-time.Second),
			),
			valid: true,
		},
		{
			name: "type_mismatch with bounded future evidence", leaf: eqTrue(conditionAge(300)),
			node: node(
				automations.AutomationConditionUnknown, automations.AutomationConditionUnknownTypeMismatch,
				`"true"`, at(time.Second),
			),
			valid: false,
		},
		{
			name: "type_mismatch with bounded expired evidence", leaf: eqTrue(conditionAge(300)),
			node: node(
				automations.AutomationConditionUnknown, automations.AutomationConditionUnknownTypeMismatch,
				`"true"`, at(-301*time.Second),
			),
			valid: false,
		},
		{
			name: "pointer_missing with current evidence", leaf: eqTrue(conditionAge(300)),
			node: node(
				automations.AutomationConditionUnknown, automations.AutomationConditionUnknownPointerMissing,
				"", at(-time.Second),
			),
			valid: true,
		},
		{
			name: "pointer_missing with bounded future evidence", leaf: eqTrue(conditionAge(300)),
			node: node(
				automations.AutomationConditionUnknown, automations.AutomationConditionUnknownPointerMissing,
				"", at(time.Second),
			),
			valid: false,
		},
		{
			name: "pointer_missing with bounded expired evidence", leaf: eqTrue(conditionAge(300)),
			node: node(
				automations.AutomationConditionUnknown, automations.AutomationConditionUnknownPointerMissing,
				"", at(-301*time.Second),
			),
			valid: false,
		},
	}
	for _, testCase := range cases {
		decision := automations.AutomationConditionDecision{
			Mode:     automations.AutomationConditionDecisionEvaluated,
			Snapshot: &testCase.leaf,
			Evaluation: &automations.AutomationConditionEvaluation{
				EvaluatedAt: now,
				Result:      testCase.node.Result,
				Nodes:       []automations.AutomationConditionNodeResult{testCase.node},
			},
		}
		err := automations.ValidateAutomationConditionDecision(decision)
		if testCase.valid && err != nil {
			t.Errorf("%s: %v, want a valid decision", testCase.name, err)
		}
		if !testCase.valid && !errors.Is(err, automations.ErrInvalidAutomation) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
	}
}

func cloneEvaluationPointer(
	evaluation automations.AutomationConditionEvaluation,
) *automations.AutomationConditionEvaluation {
	cloned := cloneConditionEvaluation(evaluation)
	return &cloned
}

// Run decisions must agree with the definition snapshot, never record
// not_evaluated, bypass only a manual Run, and admit on a true root.
func TestValidateRunConditionDecision(t *testing.T) {
	t.Parallel()
	otherTree, otherEvaluation := conditionTreeAndEvaluation(
		t,
		automations.AutomationConditionTrue,
		automations.AutomationConditionFalse,
		automations.AutomationConditionTrue,
	)
	trueTree, trueEvaluation := conditionTreeAndEvaluation(
		t, automations.AutomationConditionTrue, automations.AutomationConditionTrue,
	)
	falseTree, falseEvaluation := conditionTreeAndEvaluation(
		t, automations.AutomationConditionTrue, automations.AutomationConditionFalse,
	)
	run := func(
		conditions *automations.AutomationCondition,
		source automations.RunSource,
		decision automations.AutomationConditionDecision,
	) automations.AutomationRun {
		return automations.AutomationRun{
			Source:            source,
			Snapshot:          automations.AutomationDefinition{Conditions: conditions},
			ConditionDecision: decision,
		}
	}
	valid := run(&trueTree, automations.RunSourceManual, automations.AutomationConditionDecision{
		Mode: automations.AutomationConditionDecisionEvaluated, Snapshot: &trueTree, Evaluation: &trueEvaluation,
	})
	if err := automations.ValidateRunConditionDecision(valid); err != nil {
		t.Fatalf("valid evaluated Run: %v", err)
	}
	bypassed := run(&trueTree, automations.RunSourceManual, automations.AutomationConditionDecision{
		Mode: automations.AutomationConditionDecisionBypassed, BypassRequested: true, Snapshot: &trueTree,
	})
	if err := automations.ValidateRunConditionDecision(bypassed); err != nil {
		t.Fatalf("valid bypassed Run: %v", err)
	}
	// A manual unconditioned Run may record a requested bypass; no automatic
	// outcome ever may.
	manualUnconditionedBypass := run(nil, automations.RunSourceManual, automations.AutomationConditionDecision{
		Mode: automations.AutomationConditionDecisionNotConfigured, BypassRequested: true,
	})
	if err := automations.ValidateRunConditionDecision(manualUnconditionedBypass); err != nil {
		t.Fatalf("valid manual unconditioned bypass Run: %v", err)
	}
	cases := []struct {
		name string
		run  automations.AutomationRun
	}{
		{"not evaluated", run(&trueTree, automations.RunSourceManual, automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionNotEvaluated, Snapshot: &trueTree,
		})},
		{"automatic bypass", run(&trueTree, automations.RunSourceDeviceFact, automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionBypassed, BypassRequested: true, Snapshot: &trueTree,
		})},
		{
			"automatic unconditioned bypass request",
			run(nil, automations.RunSourceDeviceFact, automations.AutomationConditionDecision{
				Mode: automations.AutomationConditionDecisionNotConfigured, BypassRequested: true,
			}),
		},
		{"evaluated false root", run(&falseTree, automations.RunSourceManual, automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionEvaluated, Snapshot: &falseTree, Evaluation: &falseEvaluation,
		})},
		{"snapshot mismatch", run(&falseTree, automations.RunSourceManual, automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionEvaluated, Snapshot: &otherTree, Evaluation: &otherEvaluation,
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
		t, automations.AutomationConditionTrue, automations.AutomationConditionFalse,
	)
	unknownTree, unknownEvaluation := conditionTreeAndEvaluation(
		t, automations.AutomationConditionTrue, automations.AutomationConditionUnknown,
	)
	skip := func(
		reason automations.AutomationSkipReason,
		decision automations.AutomationConditionDecision,
	) automations.AutomationSkip {
		return automations.AutomationSkip{Reason: reason, ConditionDecision: decision}
	}
	validFalse := skip(automations.AutomationSkipConditionsFalse, automations.AutomationConditionDecision{
		Mode: automations.AutomationConditionDecisionEvaluated, Snapshot: &falseTree, Evaluation: &falseEvaluation,
	})
	if err := automations.ValidateSkipConditionDecision(validFalse); err != nil {
		t.Fatalf("valid conditions_false Skip: %v", err)
	}
	validUnknown := skip(automations.AutomationSkipConditionsUnknown, automations.AutomationConditionDecision{
		Mode: automations.AutomationConditionDecisionEvaluated, Snapshot: &unknownTree, Evaluation: &unknownEvaluation,
	})
	if err := automations.ValidateSkipConditionDecision(validUnknown); err != nil {
		t.Fatalf("valid conditions_unknown Skip: %v", err)
	}
	validStale := skip(automations.AutomationSkipStaleFact, automations.AutomationConditionDecision{
		Mode: automations.AutomationConditionDecisionNotEvaluated, Snapshot: &falseTree,
	})
	if err := automations.ValidateSkipConditionDecision(validStale); err != nil {
		t.Fatalf("valid stale Skip: %v", err)
	}
	cases := []struct {
		name string
		skip automations.AutomationSkip
	}{
		{
			"reason result mismatch",
			skip(automations.AutomationSkipConditionsFalse, automations.AutomationConditionDecision{
				Mode:       automations.AutomationConditionDecisionEvaluated,
				Snapshot:   &falseTree,
				Evaluation: &unknownEvaluation,
			}),
		},
		{
			"condition reason without evaluation",
			skip(automations.AutomationSkipConditionsFalse, automations.AutomationConditionDecision{
				Mode: automations.AutomationConditionDecisionNotEvaluated, Snapshot: &falseTree,
			}),
		},
		{
			"stale Skip with evaluation",
			skip(automations.AutomationSkipStaleFact, automations.AutomationConditionDecision{
				Mode:       automations.AutomationConditionDecisionEvaluated,
				Snapshot:   &falseTree,
				Evaluation: &falseEvaluation,
			}),
		},
		{"busy Skip with bypass", skip(automations.AutomationSkipBusy, automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionBypassed, BypassRequested: true, Snapshot: &falseTree,
		})},
		{"unknown reason", skip(automations.AutomationSkipReason("mystery"), automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionNotEvaluated, Snapshot: &falseTree,
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
	tree, evaluation := conditionTreeAndEvaluation(t, automations.AutomationConditionTrue)
	decision := automations.AutomationConditionDecision{
		Mode: automations.AutomationConditionDecisionEvaluated, Snapshot: &tree, Evaluation: &evaluation,
	}
	raw, err := automations.EncodeAutomationConditionDecision(decision)
	if err != nil {
		t.Fatal(err)
	}
	// The original tree is mutated after persisting, representing a definition
	// edit or deletion; the persisted decision must stay self-consistent.
	tree.Children[0].ID = "renamed"
	decoded, err := automations.DecodeAutomationConditionDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Snapshot == nil || decoded.Snapshot.Children[0].ID != "leaf-a" {
		t.Fatalf("retained snapshot = %#v", decoded.Snapshot)
	}
	if err = automations.ValidateAutomationConditionDecision(decoded); err != nil {
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
		decision, err := automations.DecodeAutomationConditionDecision(json.RawMessage(raw))
		if err != nil {
			return
		}
		encoded, err := automations.EncodeAutomationConditionDecision(decision)
		if err != nil {
			t.Fatalf("accepted decision failed to encode: %v", err)
		}
		redecoded, err := automations.DecodeAutomationConditionDecision(encoded)
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
	tree, evaluation := conditionTreeAndEvaluation(t, automations.AutomationConditionTrue)
	decision := automations.AutomationConditionDecision{
		Mode: automations.AutomationConditionDecisionEvaluated, Snapshot: &tree, Evaluation: &evaluation,
	}
	first, err := automations.EncodeAutomationConditionDecision(decision)
	if err != nil {
		t.Fatal(err)
	}
	second, err := automations.EncodeAutomationConditionDecision(decision)
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
