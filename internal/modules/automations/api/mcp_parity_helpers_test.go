package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func assertCronScheduleHistory(t *testing.T, router http.Handler, session *mcp.ClientSession, id string) {
	t.Helper()
	path := "/v1/automations/" + id
	history := restJSON(t, router, path+"/history")
	assertSamePage(
		t,
		"schedule history",
		callAutomationTool(t, session, "list_automation_history", map[string]any{"automation_id": id}),
		history,
	)
	items := pageObject(t, history)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("schedule history = %v, want Run and busy Skip", items)
	}
	for _, item := range items {
		summary := pageObject(t, item)
		assertScheduleEvidenceAbsent(t, summary)
		entryID := summary["id"].(string)
		detail := restJSON(t, router, path+"/history/"+entryID)
		assertToolMatchesREST(
			t,
			callAutomationTool(
				t,
				session,
				"get_automation_history_entry",
				map[string]any{"automation_id": id, "entry_id": entryID},
			),
			detail,
		)
		kind := summary["kind"].(string)
		entry := pageObject(t, pageObject(t, detail)[kind])
		assertScheduleEvidenceAbsent(t, entry)
		if kind == "run" {
			if canonicalJSON(t, entry["matched_trigger_ids"]) != `["morning","quarter"]` {
				t.Fatalf("grouped IDs = %v", entry["matched_trigger_ids"])
			}
			assertCronDefinition(t, map[string]any{"definition": entry["snapshot"]}, "0,15 7 * * FRI")
		} else {
			if entry["reason"] != "automation_busy" {
				t.Fatalf("schedule Skip = %v", entry)
			}
			assertCronDefinition(
				t,
				map[string]any{"definition": map[string]any{"triggers": entry["matched_triggers"]}},
				"0,15 7 * * FRI",
			)
		}
	}
}

func assertToolMatchesREST(t *testing.T, tool *mcp.CallToolResult, route any) {
	t.Helper()
	if tool.IsError {
		t.Fatal(toolErrorText(t, tool))
	}
	if canonicalJSON(t, tool.StructuredContent) != canonicalJSON(t, route) {
		t.Fatalf("MCP = %s, HTTP = %s", canonicalJSON(t, tool.StructuredContent), canonicalJSON(t, route))
	}
}

func assertCronDefinition(t *testing.T, body map[string]any, expression string) {
	t.Helper()
	definition := pageObject(t, body["definition"])
	triggers := definition["triggers"].([]any)
	if len(triggers) != 2 {
		t.Fatalf("cron triggers = %v", triggers)
	}
	for index, item := range triggers {
		trigger := pageObject(t, item)
		want, wantID := expression, "morning"
		if index == 1 {
			want = "*/15 * * * *"
			wantID = "quarter"
		}
		if len(trigger) != 3 || trigger["id"] != wantID || trigger["kind"] != "cron" || trigger["expression"] != want {
			t.Fatalf("cron Trigger = %v, want exactly id, kind, expression %q", trigger, want)
		}
	}
}

func assertScheduleEvidenceAbsent(t *testing.T, body map[string]any) {
	t.Helper()
	cause, ok := body["cause"].(map[string]any)
	if !ok || cause["kind"] != "schedule" || len(cause) != 1 {
		t.Fatalf("schedule source = %v", body)
	}
	for _, field := range []string{"source", "fact", "held_state", "entity_id"} {
		if _, present := body[field]; present {
			t.Errorf("schedule history contains %s: %v", field, body)
		}
	}
}

func assertInvalidCronNoWrite(
	t *testing.T,
	router http.Handler,
	session *mcp.ClientSession,
	definition map[string]any,
	id, baseline string,
) {
	t.Helper()
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		path, payload := "/v1/automations", any(definition)
		if method == http.MethodPut {
			path += "/" + id
			payload = map[string]any{"expected_revision": 1, "definition": definition}
		}
		response := performJSON(router, method, path, canonicalJSON(t, payload))
		if response.Code != http.StatusUnprocessableEntity && response.Code != http.StatusBadRequest {
			t.Fatalf("invalid %s = %d: %s", method, response.Code, response.Body.String())
		}
		var problem map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if problem["code"] != "invalid_automation" {
			t.Fatalf("invalid %s problem = %v", method, problem)
		}
	}
	for _, tool := range []string{"create_automation", "replace_automation"} {
		arguments := map[string]any{"definition": definition}
		if tool == "replace_automation" {
			arguments["automation_id"], arguments["expected_revision"] = id, 1
		}
		result := callAutomationTool(t, session, tool, arguments)
		assertCronMCPValidationError(t, result, definition)
	}
	if got := canonicalJSON(t, restJSON(t, router, "/v1/automations/"+id)); got != baseline {
		t.Fatalf("rejected replacement changed definition: %s", got)
	}
	if ids := pageItemIDs(t, restJSON(t, router, "/v1/automations")); len(ids) != 1 || ids[0] != id {
		t.Fatalf("rejected create wrote definitions: %v", ids)
	}
	if ids := pageItemIDs(t, restJSON(t, router, "/v1/automations/"+id+"/history")); len(ids) != 0 {
		t.Fatalf("invalid input wrote history: %v", ids)
	}
}

func assertCronMCPValidationError(t *testing.T, result *mcp.CallToolResult, definition map[string]any) {
	t.Helper()
	text := toolErrorText(t, result)
	trigger := definition["triggers"].([]any)[0].(map[string]any)
	expression, isString := trigger["expression"].(string)
	structurallyValid := len(trigger) == 3 && trigger["kind"] == "cron" && isString &&
		utf8.RuneCountInString(expression) > 0 && utf8.RuneCountInString(expression) <= 512
	want := "validating"
	if structurallyValid {
		want = "invalid_automation"
	}
	if !result.IsError || !strings.Contains(text, want) {
		t.Fatalf("MCP error = %s, want %s", text, want)
	}
}

// restJSON reads one Huma route and returns its decoded JSON body, failing
// unless the route answered 200.
//
// Huma links its response bodies to the OpenAPI schema with a $schema envelope
// key; the MCP bodies are the same body without it, so it is dropped here.
func restJSON(t *testing.T, router http.Handler, path string) any {
	t.Helper()
	response := performJSON(router, http.MethodGet, path, "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d: %s", path, response.Code, response.Body.String())
	}
	var body any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode GET %s body %s: %v", path, response.Body.String(), err)
	}
	if object, ok := body.(map[string]any); ok {
		delete(object, "$schema")
	}
	return body
}

// canonicalJSON re-encodes one decoded JSON value with sorted object keys, so a
// route body and an MCP structured content that differ only in key order compare
// equal.
func canonicalJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON value: %v", err)
	}
	return string(encoded)
}

// assertSamePage proves one MCP tool result carries exactly the page one route
// returned, cursor included, and returns that shared next_cursor.
func assertSamePage(t *testing.T, label string, tool *mcp.CallToolResult, route any) string {
	t.Helper()
	if tool.IsError {
		t.Fatalf("%s tool error = %q", label, toolErrorText(t, tool))
	}
	if got, want := canonicalJSON(t, tool.StructuredContent), canonicalJSON(t, route); got != want {
		t.Fatalf("%s tool body = %s, route body = %s, want equal", label, got, want)
	}
	routeCursor := pageCursor(t, route)
	if toolCursor := pageCursor(t, tool.StructuredContent); toolCursor != routeCursor {
		t.Fatalf(
			"%s tool next_cursor = %q, route next_cursor = %q, want equal",
			label, toolCursor, routeCursor,
		)
	}
	return routeCursor
}

// pageCursor returns one page's next_cursor, or "" when the page published none.
func pageCursor(t *testing.T, body any) string {
	t.Helper()
	cursor, _ := pageObject(t, body)["next_cursor"].(string)
	return cursor
}

// pageItemIDs returns one page's item IDs in order.
func pageItemIDs(t *testing.T, body any) []string {
	t.Helper()
	items, ok := pageObject(t, body)["items"].([]any)
	if !ok {
		t.Fatalf("page body = %#v, want an items array", body)
	}
	ids := make([]string, len(items))
	for index, item := range items {
		object, isObject := item.(map[string]any)
		if !isObject {
			t.Fatalf("page item %d = %#v, want a JSON object", index, item)
		}
		id, _ := object["id"].(string)
		if id == "" {
			t.Fatalf("page item %d = %#v, want an id", index, object)
		}
		ids[index] = id
	}
	return ids
}

func pageObject(t *testing.T, body any) map[string]any {
	t.Helper()
	object, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("page body = %#v, want a JSON object", body)
	}
	return object
}
