//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
)

// Transport parity must exercise the independent MCP output DTOs, including
// immutable schedule history, rather than comparing two calls to one mapper.
func TestCronHTTPMCPRoundTripsAndScheduleHistory(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 2, 11, 59, 0, 0, time.UTC).UnixNano())
	dependencies := automations.Dependencies{
		Now: func() time.Time { return time.Unix(0, clock.Load()).UTC() }, HouseholdLocation: location,
	}
	stub := newAPIDevices()
	blocked := make(chan struct{})
	stub.block = blocked
	service := automations.NewService(
		automationssqlite.NewAutomationRepository(openAutomationTestDatabase(t), dependencies), stub, dependencies,
	)
	t.Cleanup(func() {
		close(blocked)
		if drainErr := service.Drain(context.Background()); drainErr != nil {
			t.Error(drainErr)
		}
	})
	router := echo.New()
	openapi := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	automationsapi.Register(huma.NewGroup(openapi, "/v1"), service)
	session := connectAutomationMCP(t, service)
	definition := definitionArguments(t, definitionDocument(t, 1))
	definition["triggers"] = []any{
		map[string]any{"id": "morning", "kind": "cron", "expression": "  0,15\t7  * * fri  "},
		map[string]any{"id": "quarter", "kind": "cron", "expression": "*/15 * * * *"},
	}
	created := callAutomationTool(t, session, "create_automation", map[string]any{"definition": definition})
	if created.IsError {
		t.Fatal(toolErrorText(t, created))
	}
	body := pageObject(t, created.StructuredContent)
	id := body["id"].(string)
	path := "/v1/automations/" + id
	assertCronDefinition(t, body, "0,15 7 * * fri")
	assertToolMatchesREST(t, created, restJSON(t, router, path))
	assertToolMatchesREST(
		t,
		callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": id}),
		restJSON(t, router, path),
	)

	// The other transport creates and replaces too, and neither publishes an
	// empty entity_id. Replacement preserves token spelling while normalizing separators.
	response := performJSON(router, http.MethodPost, "/v1/automations", canonicalJSON(t, definition))
	if response.Code != http.StatusCreated {
		t.Fatalf("HTTP create = %d: %s", response.Code, response.Body.String())
	}
	httpID := decodeAutomation(t, response).ID
	assertCronDefinition(t, pageObject(t, restJSON(t, router, "/v1/automations/"+httpID)), "0,15 7 * * fri")
	definition["triggers"].([]any)[0].(map[string]any)["expression"] = " 0,15\t7 * * FRI "
	definition["enabled"] = false
	response = performJSON(
		router,
		http.MethodPut,
		"/v1/automations/"+httpID,
		canonicalJSON(t, map[string]any{"expected_revision": 1, "definition": definition}),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP replace = %d: %s", response.Code, response.Body.String())
	}
	assertToolMatchesREST(
		t,
		callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": httpID}),
		restJSON(t, router, "/v1/automations/"+httpID),
	)
	definition["enabled"] = true
	assertCronDefinition(t, pageObject(t, restJSON(t, router, "/v1/automations/"+httpID)), "0,15 7 * * FRI")
	replaced := callAutomationTool(
		t,
		session,
		"replace_automation",
		map[string]any{"automation_id": id, "expected_revision": 1, "definition": definition},
	)
	assertToolMatchesREST(t, replaced, restJSON(t, router, path))
	assertCronDefinition(t, pageObject(t, replaced.StructuredContent), "0,15 7 * * FRI")
	if pageObject(t, replaced.StructuredContent)["revision"] != float64(2) {
		t.Fatal("MCP replacement did not advance revision")
	}
	if err = service.InitializeSchedules(context.Background(), dependencies.Now()); err != nil {
		t.Fatal(err)
	}
	clock.Store(time.Date(2026, 10, 2, 12, 0, 20, 0, time.UTC).UnixNano())
	if _, err = service.ProcessDueSchedules(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Store(time.Date(2026, 10, 2, 12, 15, 20, 0, time.UTC).UnixNano())
	if _, err = service.ProcessDueSchedules(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCronScheduleHistory(t, router, session, id)
}

// Core parser restrictions must survive both transport adapters. Rejected
// creates and replacements leave the real SQLite definition and history intact.
//
//nolint:paralleltest,tparallel // Subtests deliberately share one revisioned definition and assert no writes between requests.
func TestCronInvalidDefinitionsHTTPMCPDoNotWrite(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	definition := definitionArguments(t, definitionDocument(t, 1))
	definition["triggers"] = []any{map[string]any{"id": "daily", "kind": "cron", "expression": "0 7 * * *"}}
	response := performJSON(router, http.MethodPost, "/v1/automations", canonicalJSON(t, definition))
	if response.Code != http.StatusCreated {
		t.Fatalf("create valid baseline = %d: %s", response.Code, response.Body.String())
	}
	id := decodeAutomation(t, response).ID
	path := "/v1/automations/" + id
	baseline := canonicalJSON(t, restJSON(t, router, path))
	cases := []struct {
		name       string
		expression any
	}{
		{"empty", ""}, {"null", nil}, {"number", 7}, {"byte limit", strings.Repeat(" ", 513)},
		{"UTF-8 byte limit before normalization", strings.Repeat("\u2003", 170) + "0 7 * * *"},
		{"calendar date", "0 7 1 * *"}, {"calendar step", "0 7 * */1 *"},
		{"seconds", "0 0 7 * * *"}, {"year", "0 0 7 * * * 2026"}, {"too few fields", "0 7 * *"},
		{"descriptor", "@daily"}, {"interval", "@every 1h"}, {"timezone", "TZ=UTC 0 7 * * *"},
		{"cron timezone", "CRON_TZ=UTC 0 7 * * *"}, {"question", "0 7 * * ?"},
		{"last", "0 7 * * L"}, {"weekday extension", "0 7 * * 1W"}, {"nth weekday", "0 7 * * MON#2"},
		{"minute bound", "60 7 * * *"}, {"hour bound", "0 24 * * *"}, {"Sunday seven", "0 7 * * 7"},
		{"zero step", "*/0 7 * * *"}, {"wrapping range", "0 7 * * FRI-MON"},
		{"empty comma item", "0, 7 * * *"}, {"bare step", "/15 7 * * *"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			definition["triggers"] = []any{map[string]any{"id": "daily", "kind": "cron", "expression": test.expression}}
			assertInvalidCronNoWrite(t, router, session, definition, id, baseline)
		})
	}
	for _, field := range []string{"entity_id", "for_seconds", "comparisons", "local_time", "weekdays", "hours", "minutes", "seconds", "dispositions"} {
		t.Run("other family "+field, func(t *testing.T) {
			definition["triggers"] = []any{
				map[string]any{"id": "daily", "kind": "cron", "expression": "0 7 * * *", field: nil},
			}
			assertInvalidCronNoWrite(t, router, session, definition, id, baseline)
		})
	}
	for _, kind := range []string{"clock_time", "time_pattern"} {
		t.Run("unimplemented "+kind, func(t *testing.T) {
			definition["triggers"] = []any{map[string]any{"id": "daily", "kind": kind, "expression": "0 7 * * *"}}
			assertInvalidCronNoWrite(t, router, session, definition, id, baseline)
		})
	}
	t.Run("missing expression", func(t *testing.T) {
		definition["triggers"] = []any{map[string]any{"id": "daily", "kind": "cron"}}
		assertInvalidCronNoWrite(t, router, session, definition, id, baseline)
	})
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
	var run AutomationRunBody
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
