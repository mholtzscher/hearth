//go:build integration

package mcpapi_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestBranchingExactDefinitionAndDirectHistory(t *testing.T) {
	t.Parallel()
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	stub := newAPIDevices()
	stub.setEntityStateSnapshot(apiStateSnapshot(t, entity, branchExactNumber))
	service := newAutomationService(t, stub)
	session := connectAutomationMCP(t, service)
	document := nestedBranchDocument(t, entity)
	created := exactToolBody(
		t,
		callAutomationTool(t, session, "create_automation", json.RawMessage(`{"definition":`+document+`}`)),
	)
	assertMembers(t, created, "automation_id", "revision")
	id := created["automation_id"].(string)
	read := exactToolBody(t, callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": id}))
	assertMembers(t, read, "automation_id", "revision", "created_at", "updated_at", "definition")
	assertNestedExactDefinition(t, read)
	page := exactToolBody(t, callAutomationTool(t, session, "list_automations", map[string]any{}))
	summary := page["items"].([]any)[0].(map[string]any)
	assertMembers(t, summary, "automation_id", "name", "enabled", "revision")
	if summary["name"] != "Nested" || summary["enabled"] != true || summary["revision"] != json.Number("1") {
		t.Fatalf("summary = %v", summary)
	}
	replaced := exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"replace_automation",
			json.RawMessage(fmt.Sprintf(`{"automation_id":%q,"expected_revision":1,"definition":%s}`, id, document)),
		),
	)
	assertMembers(t, replaced, "automation_id", "revision")
	if replaced["revision"] != json.Number("2") {
		t.Fatalf("replace receipt = %v", replaced)
	}
	read = exactToolBody(t, callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": id}))
	assertNestedExactDefinition(t, read)
	started := exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"start_automation_run",
			map[string]any{"automation_id": id, "bypass_conditions": true},
		),
	)
	assertMembers(t, started, "automation_id", "run_id", "status")
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
	assertMembers(
		t,
		run,
		"entry_id",
		"automation_id",
		"automation_name",
		"kind",
		"revision",
		"recorded_at",
		"cause",
		"condition_decision",
		"status",
		"started_at",
		"completed_at",
		"snapshot",
		"matched_trigger_ids",
		"executions",
	)
	assertNestedExactDefinition(t, map[string]any{"definition": run["snapshot"]})
	decisions := mcpBranchExecutions(t, run["executions"].([]any))
	if run["kind"] != "run" || run["entry_id"] != runID || run["status"] != "succeeded" || len(decisions) != 2 {
		t.Fatalf("direct run = %v", run)
	}
	route := decisions[0]
	if route.execution["step_id"] != "route" || route.evidence["outcome"] != "branch" ||
		route.evidence["selected_branch_id"] != "manual" {
		t.Fatalf("selection = %v", route)
	}
	for _, evaluation := range route.evidence["evaluations"].([]any) {
		node := evaluation.(map[string]any)["evaluation"].(map[string]any)["nodes"].([]any)[0].(map[string]any)
		if node["kind"] != "trigger" || canonicalJSON(t, node["matched_trigger_ids"]) != "[]" {
			t.Fatalf("manual trigger evidence = %v", node)
		}
	}
	level := decisions[1]
	node := level.evidence["evaluations"].([]any)[0].(map[string]any)["evaluation"].(map[string]any)["nodes"].([]any)[0].(map[string]any)
	if level.evidence["outcome"] != "then" || node["selected_value"] != json.Number(branchExactNumber) {
		t.Fatalf("exact evidence = %v", level)
	}
}

func TestBranchingTriggeredEvidence(t *testing.T) {
	t.Parallel()
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	document := strings.Replace(
		nestedBranchDocument(t, entity),
		`{"id":"warm","kind":"cron","expression":"* * * * *"}`,
		fmt.Sprintf(`{"id":"warm","kind":"observation","entity_id":%q,"dispositions":["applied"]}`, entity),
		1,
	)
	created := exactToolBody(
		t,
		callAutomationTool(t, session, "create_automation", json.RawMessage(`{"definition":`+document+`}`)),
	)
	id := created["automation_id"].(string)
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := service.ReceiveDeviceFact(
		t.Context(),
		automations.ObservationFact{
			FactID:        factID,
			ObservationID: observationID,
			EntityID:      entity,
			Disposition:   devices.DispositionApplied,
			Value:         devices.Value("true"),
			EmittedAt:     time.Now().UTC(),
		},
	)
	if err != nil || outcome.StartedRuns != 1 {
		t.Fatalf("admission = %+v, %v", outcome, err)
	}
	page := exactToolBody(
		t,
		callAutomationTool(t, session, "list_automation_history", map[string]any{"automation_id": id}),
	)
	summary := page["items"].([]any)[0].(map[string]any)
	assertMembers(t, summary, "entry_id", "automation_id", "kind", "revision", "recorded_at", "source", "status")
	if summary["source"] != "device_fact" {
		t.Fatalf("source = %v", summary)
	}
	runID := summary["entry_id"].(string)
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
	decisions := mcpBranchExecutions(t, run["executions"].([]any))
	if len(decisions) != 1 {
		t.Fatalf("automatic decisions = %v", decisions)
	}
	decision := decisions[0]
	if decision.evidence["selected_branch_id"] != "automatic" {
		t.Fatalf("automatic selection = %v", decision)
	}
	evaluations := decision.evidence["evaluations"].([]any)
	if len(evaluations) != 1 {
		t.Fatalf("automatic evaluations = %v", evaluations)
	}
	node := evaluations[0].(map[string]any)["evaluation"].(map[string]any)["nodes"].([]any)[0].(map[string]any)
	if node["kind"] != "trigger" || node["result"] != "true" ||
		canonicalJSON(t, node["matched_trigger_ids"]) != `["warm"]` ||
		len(node) != 4 {
		t.Fatalf("trigger evidence = %v", node)
	}
}

type mcpBranchExecution struct {
	execution map[string]any
	evidence  map[string]any
}

func mcpBranchExecutions(t *testing.T, executions []any) []mcpBranchExecution {
	t.Helper()
	var branches []mcpBranchExecution
	for _, item := range executions {
		execution := item.(map[string]any)
		if execution["kind"] != "if" && execution["kind"] != "choose" {
			continue
		}
		evidence, ok := execution["evidence"].(map[string]any)
		if !ok {
			t.Fatalf("branch execution lacks evidence: %v", execution)
		}
		branches = append(branches, mcpBranchExecution{execution: execution, evidence: evidence})
	}
	return branches
}

//nolint:paralleltest,tparallel // One revisioned baseline establishes rejection without writes.
func TestBranchingInvalidCreateAndReplaceDoNotWrite(t *testing.T) {
	t.Parallel()
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	document := nestedBranchDocument(t, entity)
	created := exactToolBody(
		t,
		callAutomationTool(t, session, "create_automation", json.RawMessage(`{"definition":`+document+`}`)),
	)
	id := created["automation_id"].(string)
	cases := map[string]string{
		"missing command kind": strings.Replace(document, `"kind":"command",`, ``, 1),
		"mixed fields":         strings.Replace(document, `"kind":"if"`, `"kind":"if","operation":"set"`, 1),
		"delay on branch":      strings.Replace(document, `"kind":"if"`, `"kind":"if","duration_ms":1`, 1),
		"unknown":              strings.Replace(document, `"kind":"if"`, `"kind":"if","surprise":true`, 1),
		"null":                 strings.Replace(document, `"kind":"if"`, `"kind":"if","else":null`, 1),
		"empty":                strings.Replace(document, `"kind":"if"`, `"kind":"if","else":[]`, 1),
		"bad trigger":          strings.ReplaceAll(document, `["warm"]`, `["missing"]`),
		"duplicate step":       strings.Replace(document, `"id":"selected"`, `"id":"unselected"`, 1),
		"duplicate branch":     strings.Replace(document, `"id":"manual"`, `"id":"automatic"`, 1),
		"admission trigger": strings.Replace(
			document,
			`"steps":[`,
			`"conditions":{"id":"invalid","kind":"trigger","trigger_ids":["warm"]},"steps":[`,
			1,
		),
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) { assertDefinitionWriteRejected(t, session, id, bad) })
	}
	page, err := service.ListAutomations(t.Context(), automations.ListAutomationsParams{Limit: 50})
	if err != nil || len(page.Items) != 1 || page.Items[0].Revision != 1 {
		t.Fatalf("rejected writes = %+v, %v", page, err)
	}
}
