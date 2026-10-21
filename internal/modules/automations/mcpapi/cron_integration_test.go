//go:build integration

package mcpapi_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestCronRoundTripAndScheduleHistory(t *testing.T) {
	t.Parallel()
	location, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 2, 11, 59, 0, 0, time.UTC).UnixNano())
	deps := automations.Dependencies{
		Now:               func() time.Time { return time.Unix(0, clock.Load()).UTC() },
		HouseholdLocation: location,
	}
	stub := newAPIDevices()
	blocked := make(chan struct{})
	stub.block = blocked
	service := newAutomationService(t, stub, deps)
	t.Cleanup(func() {
		close(blocked)
		if drainErr := service.Drain(context.Background()); drainErr != nil {
			t.Error(drainErr)
		}
	})
	session := connectAutomationMCP(t, service)
	definition := definitionArguments(t, definitionDocument(t, 1))
	definition["triggers"] = []any{
		map[string]any{"id": "morning", "kind": "cron", "expression": "  0,15\t7  * * fri  "},
		map[string]any{"id": "quarter", "kind": "cron", "expression": "*/15 * * * *"},
	}
	created := exactToolBody(
		t,
		callAutomationTool(t, session, "create_automation", map[string]any{"definition": definition}),
	)
	id := created["automation_id"].(string)
	read := exactToolBody(t, callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": id}))
	assertCronDefinition(t, read, "0,15 7 * * fri")
	definition["triggers"].([]any)[0].(map[string]any)["expression"] = " 0,15\t7 * * FRI "
	exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"replace_automation",
			map[string]any{"automation_id": id, "expected_revision": 1, "definition": definition},
		),
	)
	read = exactToolBody(t, callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": id}))
	assertCronDefinition(t, read, "0,15 7 * * FRI")
	if err = service.InitializeSchedules(t.Context(), deps.Now()); err != nil {
		t.Fatal(err)
	}
	for _, minute := range []int{0, 15} {
		clock.Store(time.Date(2026, 10, 2, 12, minute, 20, 0, time.UTC).UnixNano())
		if _, err = service.ProcessDueSchedules(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	assertCronHistory(t, session, id)
}

// Core parser restrictions must survive both transport adapters. Rejected
// creates and replacements leave the real SQLite definition and history intact.
//
//nolint:paralleltest,tparallel // Subtests deliberately share one revisioned definition and assert no writes between requests.
func TestCronInvalidDefinitionsDoNotWrite(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	definition := definitionArguments(t, definitionDocument(t, 1))
	definition["triggers"] = []any{map[string]any{"id": "daily", "kind": "cron", "expression": "0 7 * * *"}}
	created := exactToolBody(
		t,
		callAutomationTool(t, session, "create_automation", map[string]any{"definition": definition}),
	)
	id := created["automation_id"].(string)
	baseline := canonicalJSON(
		t,
		exactToolBody(t, callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": id})),
	)
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
			assertInvalidCronNoWrite(t, session, definition, id, baseline)
		})
	}
	for _, field := range []string{"entity_id", "for_seconds", "comparisons", "local_time", "weekdays", "hours", "minutes", "seconds", "dispositions"} {
		t.Run("other family "+field, func(t *testing.T) {
			definition["triggers"] = []any{
				map[string]any{"id": "daily", "kind": "cron", "expression": "0 7 * * *", field: nil},
			}
			assertInvalidCronNoWrite(t, session, definition, id, baseline)
		})
	}
	for _, kind := range []string{"clock_time", "time_pattern"} {
		t.Run("unimplemented "+kind, func(t *testing.T) {
			definition["triggers"] = []any{map[string]any{"id": "daily", "kind": kind, "expression": "0 7 * * *"}}
			assertInvalidCronNoWrite(t, session, definition, id, baseline)
		})
	}
	t.Run("missing expression", func(t *testing.T) {
		definition["triggers"] = []any{map[string]any{"id": "daily", "kind": "cron"}}
		assertInvalidCronNoWrite(t, session, definition, id, baseline)
	})
}

func assertCronHistory(t *testing.T, session *mcp.ClientSession, id string) {
	t.Helper()
	page := exactToolBody(
		t,
		callAutomationTool(t, session, "list_automation_history", map[string]any{"automation_id": id}),
	)
	items := page["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("schedule history = %v", page)
	}
	kinds := map[string]bool{}
	for _, raw := range items {
		summary := raw.(map[string]any)
		if summary["source"] != "schedule" {
			t.Fatalf("schedule source = %v", summary)
		}
		entryID := summary["entry_id"].(string)
		detail := exactToolBody(
			t,
			callAutomationTool(
				t,
				session,
				"get_automation_history_entry",
				map[string]any{"automation_id": id, "entry_id": entryID},
			),
		)
		assertScheduleEvidenceAbsent(t, detail)
		kind := detail["kind"].(string)
		kinds[kind] = true
		if kind == "run" {
			if canonicalJSON(t, detail["matched_trigger_ids"]) != `["morning","quarter"]` {
				t.Fatalf("grouped triggers = %v", detail)
			}
			assertCronDefinition(t, map[string]any{"definition": detail["snapshot"]}, "0,15 7 * * FRI")
		} else {
			if detail["reason"] != "automation_busy" {
				t.Fatalf("schedule skip = %v", detail)
			}
			assertMembers(
				t,
				summary,
				"entry_id",
				"automation_id",
				"kind",
				"revision",
				"recorded_at",
				"source",
				"reason",
			)
			assertCronDefinition(
				t,
				map[string]any{"definition": map[string]any{"triggers": detail["matched_triggers"]}},
				"0,15 7 * * FRI",
			)
		}
	}
	if !kinds["run"] || !kinds["skip"] {
		t.Fatalf("schedule kinds = %v", kinds)
	}
}
