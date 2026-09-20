package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
)

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

// TestAutomationMCPListAutomationsMatchesREST proves list_automations and
// GET /v1/automations return one page for one limit, resolve an omitted limit to
// the same default, and publish a cursor each surface accepts, so the two
// transports cannot drift on the page default, the keyset cursor, or the body.
func TestAutomationMCPListAutomationsMatchesREST(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := connectAutomationMCP(t, service)

	var created []string
	for index := range 3 {
		response := performJSON(router, http.MethodPost, "/v1/automations",
			strings.Replace(definitionDocument(t, 1), "Office light", fmt.Sprintf("Automation %d", index), 1))
		if response.Code != http.StatusCreated {
			t.Fatalf("create %d status = %d: %s", index, response.Code, response.Body.String())
		}
		created = append(created, decodeAutomation(t, response).ID)
	}

	assertSamePage(t, "default page",
		callAutomationTool(t, session, "list_automations", map[string]any{}),
		restJSON(t, router, "/v1/automations"))

	routeFirst := restJSON(t, router, "/v1/automations?limit=2")
	toolFirst := callAutomationTool(t, session, "list_automations", map[string]any{"limit": 2})
	routeCursor := assertSamePage(t, "first page", toolFirst, routeFirst)
	toolCursor := pageCursor(t, toolFirst.StructuredContent)
	if routeCursor == "" {
		t.Fatal("first page published no next_cursor")
	}

	// Each surface accepts the cursor the other published, so the cursor is one
	// shared scheme rather than two bodies that happen to agree.
	routeSecond := restJSON(t, router, "/v1/automations?limit=2&cursor="+toolCursor)
	toolSecond := callAutomationTool(t, session, "list_automations",
		map[string]any{"limit": 2, "cursor": routeCursor})
	assertSamePage(t, "second page", toolSecond, routeSecond)

	firstPage := pageItemIDs(t, routeFirst)
	secondPage := pageItemIDs(t, routeSecond)
	if !slices.Equal(firstPage, created[:2]) || !slices.Equal(secondPage, created[2:]) {
		t.Fatalf(
			"paged IDs = %#v then %#v, want the creation order %#v without overlap",
			firstPage, secondPage, created,
		)
	}
}

// TestAutomationMCPListAutomationHistoryMatchesREST proves list_automation_history
// and GET /v1/automations/{automation_id}/history return one newest-first page
// for one limit, resolve an omitted limit to the same default, and accept each
// other's cursor.
func TestAutomationMCPListAutomationHistoryMatchesREST(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	created := decodeAutomation(t, performJSON(
		router, http.MethodPost, "/v1/automations", definitionDocument(t, 1),
	))

	var started []string
	for range 3 {
		response := performJSON(router, http.MethodPost, "/v1/automations/"+created.ID+"/runs", "")
		if response.Code != http.StatusAccepted {
			t.Fatalf("start run status = %d: %s", response.Code, response.Body.String())
		}
		var run automationsapi.AutomationRunBody
		if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
			t.Fatalf("decode run body %s: %v", response.Body.String(), err)
		}
		waitForAPI(t, service, created.ID, run.ID)
		started = append(started, run.ID)
	}
	newestFirst := slices.Clone(started)
	slices.Reverse(newestFirst)

	historyPath := "/v1/automations/" + created.ID + "/history"
	assertSamePage(t, "default history page",
		callAutomationTool(t, session, "list_automation_history",
			map[string]any{"automation_id": created.ID}),
		restJSON(t, router, historyPath))

	routeFirst := restJSON(t, router, historyPath+"?limit=1")
	toolFirst := callAutomationTool(t, session, "list_automation_history",
		map[string]any{"automation_id": created.ID, "limit": 1})
	routeCursor := assertSamePage(t, "first history page", toolFirst, routeFirst)
	toolCursor := pageCursor(t, toolFirst.StructuredContent)
	if routeCursor == "" {
		t.Fatal("first history page published no next_cursor")
	}

	routeSecond := restJSON(t, router, historyPath+"?limit=1&cursor="+toolCursor)
	toolSecond := callAutomationTool(t, session, "list_automation_history",
		map[string]any{"automation_id": created.ID, "limit": 1, "cursor": routeCursor})
	secondCursor := assertSamePage(t, "second history page", toolSecond, routeSecond)

	routeThird := restJSON(t, router, historyPath+"?limit=1&cursor="+secondCursor)
	toolThird := callAutomationTool(t, session, "list_automation_history",
		map[string]any{"automation_id": created.ID, "limit": 1, "cursor": secondCursor})
	assertSamePage(t, "third history page", toolThird, routeThird)

	var walked []string
	for _, page := range []any{routeFirst, routeSecond, routeThird} {
		walked = append(walked, pageItemIDs(t, page)...)
	}
	if !slices.Equal(walked, newestFirst) {
		t.Fatalf("history pages = %#v, want the newest-first order %#v", walked, newestFirst)
	}
	if cursor := pageCursor(t, routeThird); cursor != "" {
		t.Fatalf("final history page next_cursor = %q, want none", cursor)
	}
}

// TestAutomationMCPResourceBodiesMatchTheirRoutes proves every automation
// resource reads through the same Huma operation as the matching route, so the
// document a client attaches as context is the route's body verbatim.
func TestAutomationMCPResourceBodiesMatchTheirRoutes(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	created := decodeAutomation(t, performJSON(
		router, http.MethodPost, "/v1/automations", definitionDocument(t, 1),
	))
	runResponse := performJSON(router, http.MethodPost, "/v1/automations/"+created.ID+"/runs", "")
	var run automationsapi.AutomationRunBody
	if err := json.Unmarshal(runResponse.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run body %s: %v", runResponse.Body.String(), err)
	}
	waitForAPI(t, service, created.ID, run.ID)

	historyPath := "/v1/automations/" + created.ID + "/history"
	tests := []struct {
		name string
		uri  string
		path string
	}{
		{"definition", "hearth://automation/" + created.ID, "/v1/automations/" + created.ID},
		{"history page", "hearth://automation/" + created.ID + "/history?limit=1", historyPath + "?limit=1"},
		{"collection page", "hearth://automations?limit=1", "/v1/automations?limit=1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			text := readAutomationResource(t, session, test.uri)
			var resourceBody any
			if err := json.Unmarshal([]byte(text), &resourceBody); err != nil {
				t.Fatalf("decode resource body %s: %v", text, err)
			}
			got, want := canonicalJSON(t, resourceBody), canonicalJSON(t, restJSON(t, router, test.path))
			if got != want {
				t.Fatalf("resource %s body = %s, route body = %s, want equal", test.uri, got, want)
			}
		})
	}
}

// TestAutomationMCPListCursorFailuresMatchREST proves an unreadable cursor fails
// the same way on both surfaces: the route answers 400 with the invalid_cursor
// problem code and its reason, and the tool reports that code and reason as its
// failure fields, because one handler decodes the cursor for both.
func TestAutomationMCPListCursorFailuresMatchREST(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	created := decodeAutomation(t, performJSON(
		router, http.MethodPost, "/v1/automations", definitionDocument(t, 1),
	))

	cases := []struct {
		name      string
		route     string
		tool      string
		arguments map[string]any
		detail    string
	}{
		{
			"automations cursor",
			"/v1/automations?cursor=not-a-cursor",
			"list_automations",
			map[string]any{"cursor": "not-a-cursor"},
			"automation cursor is invalid",
		},
		{
			"history cursor",
			"/v1/automations/" + created.ID + "/history?cursor=not-a-cursor",
			"list_automation_history",
			map[string]any{"automation_id": created.ID, "cursor": "not-a-cursor"},
			"history cursor is invalid",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := performJSON(router, http.MethodGet, test.route, "")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("GET %s status = %d, want 400: %s", test.route, response.Code, response.Body.String())
			}
			var problem struct {
				Code   string `json:"code"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatalf("decode %s problem %s: %v", test.route, response.Body.String(), err)
			}
			if problem.Code != "invalid_cursor" || problem.Detail != test.detail {
				t.Fatalf("GET %s problem = %#v, want invalid_cursor with %q", test.route, problem, test.detail)
			}

			result := callAutomationTool(t, session, test.tool, test.arguments)
			fields := decodeStructuredInto[struct {
				FailureCode string `json:"failure_code"`
				Message     string `json:"message"`
			}](t, result)
			if fields.FailureCode != problem.Code || fields.Message != problem.Detail {
				t.Fatalf("%s failure = %#v, want the %#v problem", test.tool, fields, problem)
			}
			if text := toolErrorText(t, result); !strings.HasPrefix(text, problem.Code+": ") {
				t.Fatalf("%s error text = %q, want the %q prefix", test.tool, text, problem.Code)
			}
		})
	}
}
