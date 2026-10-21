//go:build integration

package mcpapi_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestEvaluatedConditionRetainsSelectedNull(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	service := newAutomationService(t, stub)
	session := connectAutomationMCP(t, service)
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	definition := definitionArguments(t, conditionalDefinitionDocument(t, entity))
	definition["conditions"].(map[string]any)["operand"] = nil
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	created := exactToolBody(
		t,
		callAutomationTool(t, session, "create_automation", json.RawMessage(`{"definition":`+string(raw)+`}`)),
	)
	id := created["automation_id"].(string)
	stub.setEntityStateSnapshot(apiStateSnapshot(t, entity, `{"mode":null}`))
	started := exactToolBody(
		t,
		callAutomationTool(t, session, "start_automation_run", map[string]any{"automation_id": id}),
	)
	runID := started["run_id"].(string)
	waitForAPI(t, service, id, runID)
	run := exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"get_automation_history_entry",
			map[string]any{"automation_id": id, "entry_id": runID},
		),
	)
	decision := run["condition_decision"].(map[string]any)
	if decision["mode"] != "evaluated" || decision["bypass_requested"] != false {
		t.Fatalf("decision = %v", decision)
	}
	evaluation := decision["evaluation"].(map[string]any)
	node := evaluation["nodes"].([]any)[0].(map[string]any)
	selected, present := node["selected_value"]
	if evaluation["result"] != "true" || node["result"] != "true" || !present || selected != nil ||
		node["observation_id"] == nil {
		t.Fatalf("selected null evidence = %v", evaluation)
	}
}

func TestConditionSnapshotFailureAndBypass(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	service := newAutomationService(t, stub)
	session := connectAutomationMCP(t, service)
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	created := exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"create_automation",
			map[string]any{"definition": definitionArguments(t, conditionalDefinitionDocument(t, entity))},
		),
	)
	id := created["automation_id"].(string)
	stub.setEntityStateSnapshotError(errors.New("private snapshot outage"))
	failed := callAutomationTool(t, session, "start_automation_run", map[string]any{"automation_id": id})
	failure := decodeStructuredInto[map[string]any](t, failed)
	if !failed.IsError || failure["failure_code"] != "internal_error" || failure["message"] != "internal error" {
		t.Fatalf("snapshot failure = %v", failure)
	}
	if stub.executionCount() != 0 || len(stub.snapshotRequests()) != 1 {
		t.Fatalf("snapshot effects: commands=%d reads=%v", stub.executionCount(), stub.snapshotRequests())
	}
	page := exactToolBody(
		t,
		callAutomationTool(t, session, "list_automation_history", map[string]any{"automation_id": id}),
	)
	if len(page["items"].([]any)) != 0 {
		t.Fatalf("snapshot failure wrote history: %v", page)
	}
	run := exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"start_automation_run",
			map[string]any{"automation_id": id, "bypass_conditions": true},
		),
	)
	runID := run["run_id"].(string)
	waitForAPI(t, service, id, runID)
	if len(stub.snapshotRequests()) != 1 || stub.executionCount() != 1 {
		t.Fatalf("bypass effects: commands=%d reads=%v", stub.executionCount(), stub.snapshotRequests())
	}
	detail := exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"get_automation_history_entry",
			map[string]any{"automation_id": id, "entry_id": runID},
		),
	)
	decision := detail["condition_decision"].(map[string]any)
	if decision["mode"] != "bypassed" || decision["bypass_requested"] != true || decision["snapshot"] == nil {
		t.Fatalf("bypass evidence = %v", decision)
	}
}
