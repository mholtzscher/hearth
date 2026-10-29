//go:build integration

package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/httpapi"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// HTTP writes normalize cron separators while retaining token spelling, and
// schedule history keeps the immutable Trigger evidence for Runs and busy Skips.
func TestCronHTTPRoundTripsAndScheduleHistory(t *testing.T) {
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
	started := make(chan struct{})
	stub.block = blocked
	stub.onExecute = func(devices.CommandInput) { close(started) }
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
	automationsapi.Register(huma.NewGroup(openapi, "/v1"), service, nil)
	session := newHTTPScenario(t, service)
	definition := definitionArguments(t, definitionDocument(t, 1))
	definition["triggers"] = []any{
		map[string]any{"id": "morning", "kind": "cron", "expression": "  0,15\t7  * * fri  "},
		map[string]any{"id": "quarter", "kind": "cron", "expression": "*/15 * * * *"},
	}
	created := callHTTPScenario(t, session, "create-automation", map[string]any{"definition": definition})
	if created.Failed {
		t.Fatal(httpScenarioErrorText(t, created))
	}
	body := pageObject(t, created.Body)
	id := body["id"].(string)
	path := "/v1/automations/" + id
	assertCronDefinition(t, body, "0,15 7 * * fri")
	assertScenarioMatchesRoute(t, created, restJSON(t, router, path))
	assertScenarioMatchesRoute(
		t,
		callHTTPScenario(t, session, "get-automation", map[string]any{"automation_id": id}),
		restJSON(t, router, path),
	)

	// Exercise direct HTTP create and replace requests as well as the scenario
	// client. Neither may invent an entity_id for cron Triggers.
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
	assertScenarioMatchesRoute(
		t,
		callHTTPScenario(t, session, "get-automation", map[string]any{"automation_id": httpID}),
		restJSON(t, router, "/v1/automations/"+httpID),
	)
	definition["enabled"] = true
	assertCronDefinition(t, pageObject(t, restJSON(t, router, "/v1/automations/"+httpID)), "0,15 7 * * FRI")
	replaced := callHTTPScenario(
		t,
		session,
		"replace-automation",
		map[string]any{"automation_id": id, "expected_revision": 1, "definition": definition},
	)
	assertScenarioMatchesRoute(t, replaced, restJSON(t, router, path))
	assertCronDefinition(t, pageObject(t, replaced.Body), "0,15 7 * * FRI")
	if pageObject(t, replaced.Body)["revision"] != float64(2) {
		t.Fatal("HTTP replacement did not advance revision")
	}
	if err = service.InitializeSchedules(context.Background(), dependencies.Now()); err != nil {
		t.Fatal(err)
	}
	clock.Store(time.Date(2026, 10, 2, 12, 0, 20, 0, time.UTC).UnixNano())
	if _, err = service.ProcessDueSchedules(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The blocked external call follows the durable Command reservation. Both
	// history requests must observe that stable state, not race the worker's start.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduled Command did not reach the blocked execution")
	}
	clock.Store(time.Date(2026, 10, 2, 12, 15, 20, 0, time.UTC).UnixNano())
	if _, err = service.ProcessDueSchedules(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCronScheduleHistory(t, router, session, id)
}

// HTTP parser rejections leave the real SQLite definition, revision, and history
// intact for both create and replacement requests.
//
//nolint:paralleltest,tparallel // Subtests deliberately share one revisioned definition and assert no writes between requests.
func TestCronInvalidDefinitionsHTTPDoNotWrite(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := newHTTPScenario(t, service)
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
