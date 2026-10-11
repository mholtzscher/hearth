package api //nolint:testpackage // Exercise the shared public mapping.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestHeldStateTriggerAndEvidencePublicMapping(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	trigger := automations.Trigger{ID: "held", Body: automations.HeldStateTrigger{
		EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab", ForSeconds: 60,
		Comparisons: []automations.ObservationComparison{{
			Pointer: "", Operator: automations.ComparisonEqual, Operand: json.RawMessage(`true`),
		}},
	}}
	raw, err := json.Marshal(matchedTriggersBody([]automations.Trigger{trigger}))
	if err != nil {
		t.Fatal(err)
	}
	var triggers []struct {
		Kind        string            `json:"kind"`
		ForSeconds  int64             `json:"for_seconds"`
		Comparisons []json.RawMessage `json:"comparisons"`
	}
	if err = json.Unmarshal(raw, &triggers); err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 1 || triggers[0].Kind != "held_state" || triggers[0].ForSeconds != 60 ||
		len(triggers[0].Comparisons) != 1 {
		t.Fatalf("Trigger body = %s", raw)
	}
	evidence := automations.HeldStateEvidence{TriggerID: "held", StartedAt: started, DueAt: started.Add(time.Minute)}
	held := heldStateEvidenceBody(evidence)
	encoded, err := json.Marshal(held)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["trigger_id"]) != `"held"` || string(fields["started_at"]) != `"2026-09-23T00:00:00Z"` ||
		string(fields["due_at"]) != `"2026-09-23T00:01:00Z"` {
		t.Errorf("held-state evidence = %s", encoded)
	}
}
