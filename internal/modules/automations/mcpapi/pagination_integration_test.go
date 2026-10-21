//go:build integration

package mcpapi_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

func TestAutomationAndHistorySummaryPagination(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	var ids []string
	for i := range 3 {
		document := definitionArguments(t, definitionDocument(t, 1))
		document["name"] = fmt.Sprintf("Automation %d", i)
		created := exactToolBody(
			t,
			callAutomationTool(t, session, "create_automation", map[string]any{"definition": document}),
		)
		ids = append(ids, created["automation_id"].(string))
	}
	page := exactToolBody(t, callAutomationTool(t, session, "list_automations", map[string]any{}))
	if len(page["items"].([]any)) != 3 {
		t.Fatalf("default page = %v", page)
	}
	var walked []string
	cursor := ""
	for {
		args := map[string]any{"limit": 2}
		if cursor != "" {
			args["cursor"] = cursor
		}
		page = exactToolBody(t, callAutomationTool(t, session, "list_automations", args))
		for _, raw := range page["items"].([]any) {
			item := raw.(map[string]any)
			assertMembers(t, item, "automation_id", "name", "enabled", "revision")
			walked = append(walked, item["automation_id"].(string))
		}
		cursor = pageCursor(t, page)
		if cursor == "" {
			break
		}
	}
	if !slices.Equal(walked, ids) {
		t.Fatalf("pages = %v, want %v", walked, ids)
	}
}

func TestHistorySummaryPagination(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	created := exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"create_automation",
			map[string]any{"definition": definitionArguments(t, definitionDocument(t, 1))},
		),
	)
	ids := []string{created["automation_id"].(string)}
	var walked []string
	var cursor string
	var page map[string]any
	var runs []string
	for range 3 {
		started := exactToolBody(
			t,
			callAutomationTool(t, session, "start_automation_run", map[string]any{"automation_id": ids[0]}),
		)
		runID := started["run_id"].(string)
		waitForAPI(t, service, ids[0], runID)
		runs = append(runs, runID)
	}
	slices.Reverse(runs)
	walked = nil
	cursor = ""
	for {
		args := map[string]any{"automation_id": ids[0], "limit": 1}
		if cursor != "" {
			args["cursor"] = cursor
		}
		page = exactToolBody(t, callAutomationTool(t, session, "list_automation_history", args))
		for _, raw := range page["items"].([]any) {
			item := raw.(map[string]any)
			assertMembers(t, item, "entry_id", "automation_id", "kind", "revision", "recorded_at", "source", "status")
			if item["revision"] != json.Number("1") || item["source"] != "manual" || item["status"] != "succeeded" {
				t.Fatalf("history summary = %v", item)
			}
			walked = append(walked, item["entry_id"].(string))
		}
		cursor = pageCursor(t, page)
		if cursor == "" {
			break
		}
	}
	if !slices.Equal(walked, runs) {
		t.Fatalf("history pages = %v, want %v", walked, runs)
	}
}

func TestInvalidCursors(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	ids := []string{createdIDForInput(t)}
	for _, test := range []struct {
		tool, detail string
		args         map[string]any
	}{
		{"list_automations", "automation cursor is invalid", map[string]any{"cursor": "not-a-cursor"}},
		{"list_automation_history", "history cursor is invalid", map[string]any{"automation_id": ids[0], "cursor": "not-a-cursor"}},
	} {
		result := callAutomationTool(t, session, test.tool, test.args)
		failure := decodeStructuredInto[map[string]any](t, result)
		if !result.IsError || failure["failure_code"] != "invalid_cursor" || failure["message"] != test.detail {
			t.Fatalf("cursor failure = %v", failure)
		}
	}
}
