//go:build integration

package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// The HTTP scenario client and direct route recorder exercise the same default
// page size and cursor continuation, with independent expected creation-order IDs.
func TestAutomationHTTPListAutomationsMatchesRoutes(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := newHTTPScenario(t, service)

	var created []string
	for index := range 3 {
		response := performJSON(router, http.MethodPost, "/v1/automations",
			strings.Replace(definitionDocument(t, 1), "Office light", fmt.Sprintf("Automation %d", index), 1))
		if response.Code != http.StatusCreated {
			t.Fatalf("create %d status = %d: %s", index, response.Code, response.Body.String())
		}
		created = append(created, decodeAutomation(t, response).ID)
	}

	assertScenarioPage(t, "default page",
		callHTTPScenario(t, session, "list-automations", map[string]any{}),
		restJSON(t, router, "/v1/automations"))

	routeFirst := restJSON(t, router, "/v1/automations?limit=2")
	toolFirst := callHTTPScenario(t, session, "list-automations", map[string]any{"limit": 2})
	routeCursor := assertScenarioPage(t, "first page", toolFirst, routeFirst)
	toolCursor := pageCursor(t, toolFirst.Body)
	if routeCursor == "" {
		t.Fatal("first page published no next_cursor")
	}

	// Both HTTP request helpers must carry the published continuation unchanged.
	routeSecond := restJSON(t, router, "/v1/automations?limit=2&cursor="+toolCursor)
	toolSecond := callHTTPScenario(t, session, "list-automations",
		map[string]any{"limit": 2, "cursor": routeCursor})
	assertScenarioPage(t, "second page", toolSecond, routeSecond)

	firstPage := pageItemIDs(t, routeFirst)
	secondPage := pageItemIDs(t, routeSecond)
	if !slices.Equal(firstPage, created[:2]) || !slices.Equal(secondPage, created[2:]) {
		t.Fatalf(
			"paged IDs = %#v then %#v, want the creation order %#v without overlap",
			firstPage, secondPage, created,
		)
	}
}

// HTTP history pages keep newest-first ordering, default limits, and terminal
// cursor omission through both the scenario client and direct route recorder.
func TestAutomationHTTPListAutomationHistoryMatchesRoutes(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := newHTTPScenario(t, service)
	created := decodeAutomation(t, performJSON(
		router, http.MethodPost, "/v1/automations", definitionDocument(t, 1),
	))

	var started []string
	for range 3 {
		response := performJSON(router, http.MethodPost, "/v1/automations/"+created.ID+"/runs", "")
		if response.Code != http.StatusAccepted {
			t.Fatalf("start run status = %d: %s", response.Code, response.Body.String())
		}
		var run AutomationRunBody
		if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
			t.Fatalf("decode run body %s: %v", response.Body.String(), err)
		}
		waitForAPI(t, service, created.ID, run.ID)
		started = append(started, run.ID)
	}
	newestFirst := slices.Clone(started)
	slices.Reverse(newestFirst)

	historyPath := "/v1/automations/" + created.ID + "/history"
	assertScenarioPage(t, "default history page",
		callHTTPScenario(t, session, "list-automation-history",
			map[string]any{"automation_id": created.ID}),
		restJSON(t, router, historyPath))

	routeFirst := restJSON(t, router, historyPath+"?limit=1")
	toolFirst := callHTTPScenario(t, session, "list-automation-history",
		map[string]any{"automation_id": created.ID, "limit": 1})
	routeCursor := assertScenarioPage(t, "first history page", toolFirst, routeFirst)
	toolCursor := pageCursor(t, toolFirst.Body)
	if routeCursor == "" {
		t.Fatal("first history page published no next_cursor")
	}

	routeSecond := restJSON(t, router, historyPath+"?limit=1&cursor="+toolCursor)
	toolSecond := callHTTPScenario(t, session, "list-automation-history",
		map[string]any{"automation_id": created.ID, "limit": 1, "cursor": routeCursor})
	secondCursor := assertScenarioPage(t, "second history page", toolSecond, routeSecond)

	routeThird := restJSON(t, router, historyPath+"?limit=1&cursor="+secondCursor)
	toolThird := callHTTPScenario(t, session, "list-automation-history",
		map[string]any{"automation_id": created.ID, "limit": 1, "cursor": secondCursor})
	assertScenarioPage(t, "third history page", toolThird, routeThird)

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

// Invalid HTTP cursors return 400 and the endpoint-specific safe problem detail,
// including when requests are built through the scenario client.
func TestAutomationHTTPListCursorFailuresMatchREST(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := newHTTPScenario(t, service)
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
			"list-automations",
			map[string]any{"cursor": "not-a-cursor"},
			"automation cursor is invalid",
		},
		{
			"history cursor",
			"/v1/automations/" + created.ID + "/history?cursor=not-a-cursor",
			"list-automation-history",
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

			result := callHTTPScenario(t, session, test.tool, test.arguments)
			fields := decodeHTTPScenarioInto[struct {
				FailureCode string `json:"code"`
				Message     string `json:"detail"`
			}](t, result)
			if fields.FailureCode != problem.Code || fields.Message != problem.Detail {
				t.Fatalf("%s failure = %#v, want the %#v problem", test.tool, fields, problem)
			}
			if text := httpScenarioErrorText(t, result); !strings.HasPrefix(text, problem.Code+": ") {
				t.Fatalf("%s error text = %q, want the %q prefix", test.tool, text, problem.Code)
			}
		})
	}
}
