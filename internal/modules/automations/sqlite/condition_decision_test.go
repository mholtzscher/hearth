package sqlite_test

import (
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// A selected JSON null must stay distinct from a missing selection through one
// encoded and decoded decision, and the decoded leaf keeps its Observation time
// and identity rather than consulting newer State.
func TestConditionDecisionPreservesSelectedJSONNull(t *testing.T) {
	t.Parallel()
	entityID := newEntityID(t)
	conditions := conditionLeaf("flag", entityID, automations.ComparisonEqual, `null`)
	observedAt := admissionNow.Add(-time.Minute)
	evaluation, err := automations.EvaluateConditions(
		*conditions,
		stateSnapshotWith(presentStateEntry(t, entityID, `{"level":null}`, observedAt)),
		admissionNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluation.Nodes) != 1 ||
		string(evaluation.Nodes[0].Evidence.(automations.KnownStateEvidence).SelectedValue) != "null" {
		t.Fatalf("selected-value evidence = %#v", evaluation.Nodes)
	}
	raw, err := automations.EncodeConditionDecision(
		automations.EvaluatedDecision(*conditions, evaluation),
	)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := automations.DecodeConditionDecision(raw)
	if err != nil {
		t.Fatal(err)
	}
	leaf := decoded.DecisionEvaluation().Nodes[0].Evidence.(automations.KnownStateEvidence)
	if string(leaf.SelectedValue) != "null" {
		t.Fatalf("decoded selected value = %q, want a selected JSON null", leaf.SelectedValue)
	}
	if leaf.Observation.ObservationID == "" || !leaf.Observation.ObservedAt.Equal(observedAt) {
		t.Fatalf("decoded leaf evidence = %#v", leaf)
	}
	if decoded.DecisionEvaluation().Result != automations.ConditionTrue {
		t.Fatalf("decoded root result = %q", decoded.DecisionEvaluation().Result)
	}
}
