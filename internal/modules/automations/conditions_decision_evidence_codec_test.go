package automations_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func assertConditionDecisionEvidence(t *testing.T, decision automations.ConditionDecision) {
	t.Helper()
	evidence, err := automations.EncodePreparedConditionDecisionEvidence(decision)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(evidence, []byte(`"snapshot"`)) {
		t.Fatalf("configuration repeated in evidence: %s", evidence)
	}
	assembled, err := automations.DecodeConditionDecisionEvidence(evidence, decision.DecisionSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(assembled, decision) {
		t.Fatalf("evidence assembly = %#v, want %#v", assembled, decision)
	}
}

func TestConditionDecisionEvidenceNotConfigured(t *testing.T) {
	t.Parallel()
	raw, err := automations.EncodePreparedConditionDecisionEvidence(automations.NotConfiguredDecision())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"mode":"not_configured","bypass_requested":false}` {
		t.Fatalf("unexpected evidence: %s", raw)
	}
	decision, err := automations.DecodeConditionDecisionEvidence(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.DecisionMode() != automations.ConditionDecisionNotConfigured || decision.DecisionSnapshot() != nil {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestConditionDecisionEvidenceRejectsMalformedConfiguration(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"mode":"evaluated","bypass_requested":false,"evaluation":{}}`,
		`{"mode":"bypassed","bypass_requested":true}`,
		`{"mode":"not_evaluated","bypass_requested":false}`,
		`{"mode":"not_configured","bypass_requested":false,"snapshot":{}}`,
		`{"mode":"not_configured","bypass_requested":false,"evaluation":null}`,
		`{"mode":"not_configured","bypass_requested":true}`,
		`{}`,
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := automations.DecodeConditionDecisionEvidence(json.RawMessage(raw), nil); err == nil {
				t.Fatal("expected invalid retained evidence")
			}
		})
	}
}
