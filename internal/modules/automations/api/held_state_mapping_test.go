package api //nolint:testpackage // Exercise package-private HTTP and MCP projections.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestHeldStateTriggerAndEvidenceMapToHTTPAndMCP(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	trigger := automations.Trigger{
		ID: "held", Kind: automations.TriggerKindHeldState,
		HeldState: &automations.HeldStateTrigger{
			EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab", ForSeconds: 60,
			Comparisons: []automations.ObservationComparison{{
				Pointer: "", Operator: automations.ComparisonEqual, Operand: json.RawMessage(`true`),
			}},
		},
	}
	body := automationTriggerBody(trigger)
	if body.Kind != "held_state" || body.ForSeconds == nil || *body.ForSeconds != 60 || len(body.Comparisons) != 1 {
		t.Fatalf("HTTP Trigger body = %#v", body)
	}
	mcp := mcpTriggerOutput(body)
	if mcp.Kind != "held_state" || mcp.ForSeconds == nil || *mcp.ForSeconds != 60 || len(mcp.Comparisons) != 1 {
		t.Fatalf("MCP Trigger body = %#v", mcp)
	}
	evidence := automations.HeldStateEvidence{TriggerID: "held", StartedAt: started, DueAt: started.Add(time.Minute)}
	held := heldStateEvidenceBody(evidence)
	encoded, err := json.Marshal(map[string]any{"HTTP": held, "MCP": held})
	if err != nil {
		t.Fatal(err)
	}
	var projections map[string]map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &projections); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"HTTP", "MCP"} {
		fields := projections[name]
		if string(fields["trigger_id"]) != `"held"` || string(fields["started_at"]) == "" ||
			string(fields["due_at"]) == "" {
			t.Errorf("%s held-state evidence = %s", name, encoded)
		}
	}
}
