//go:build integration

package mcpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

//nolint:paralleltest,tparallel // One revisioned baseline proves rejection without writes.
func TestDelayInputAndDiscoveryContract(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	tools := delayTools(t, session)
	schemas := []any{
		inputSchemaDefinition(t, "create_automation", tools["create_automation"]),
		inputSchemaDefinition(t, "replace_automation", tools["replace_automation"]),
	}
	valid := delayDocument(`{"id":"wait","kind":"delay","duration_ms":{"literal":1}}`)
	created := exactToolBody(
		t,
		callAutomationTool(t, session, "create_automation", json.RawMessage(`{"definition":`+valid+`}`)),
	)
	id := created["automation_id"].(string)
	for _, duration := range []string{"1", "86400000"} {
		document := delayDocument(`{"id":"wait","kind":"delay","duration_ms":{"literal":` + duration + `}}`)
		for _, schema := range schemas {
			if err := compileDelayContractSchema(t, schema, "").Validate(exactJSONObject(t, document)); err != nil {
				t.Fatalf("valid duration %s: %v", duration, err)
			}
		}
		exactToolBody(
			t,
			callAutomationTool(t, session, "create_automation", json.RawMessage(`{"definition":`+document+`}`)),
		)
	}
	cases := map[string]string{
		"missing":        `{"id":"wait","kind":"delay"}`,
		"mixed command":  `{"id":"wait","kind":"delay","duration_ms":{"literal":1},"operation":"set"}`,
		"mixed branch":   `{"id":"wait","kind":"delay","duration_ms":{"literal":1},"then":[]}`,
		"unknown":        `{"id":"wait","kind":"delay","duration_ms":{"literal":1},"extra":true}`,
		"empty sequence": "",
	}
	for _, duration := range []string{"null", "0", "-1", "1.5", "86400001", "9223372036854775808", `"1"`} {
		cases[duration] = `{"id":"wait","kind":"delay","duration_ms":{"literal":` + duration + `}}`
	}
	var tooMany []string
	for i := range 33 {
		tooMany = append(tooMany, fmt.Sprintf(`{"id":"wait-%d","kind":"delay","duration_ms":{"literal":1}}`, i))
	}
	cases["sequence bound"] = strings.Join(tooMany, ",")
	for name, steps := range cases {
		t.Run(name, func(t *testing.T) {
			bad := delayDocument(steps)
			assertInputSchemasReject(t, schemas, bad)
			assertDefinitionWriteRejected(t, session, id, bad)
		})
	}
	assertDefinitionWriteRejected(
		t,
		session,
		id,
		delayDocument(
			`{"id":"wait","kind":"delay","duration_ms":{"literal":1}},{"id":"wait","kind":"delay","duration_ms":{"literal":2}}`,
		),
	)
	for _, bad := range delaySemanticInvalidDocuments() {
		assertDefinitionWriteRejected(t, session, id, bad)
	}
	baseline := exactToolBody(t, callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": id}))
	if baseline["revision"] != json.Number("1") ||
		canonicalJSON(t, baseline["definition"]) != canonicalJSON(t, exactJSONObject(t, valid)) {
		t.Fatalf("baseline changed: %v", baseline)
	}
	collection, err := service.ListAutomations(t.Context(), automations.ListAutomationsParams{Limit: 50})
	if err != nil || len(collection.Items) != 3 {
		t.Fatalf("rejected creates wrote records: %+v, %v", collection, err)
	}
}

func TestDelayRecursiveCompletionAndEmptyEvidence(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	service := newAutomationService(t, stub)
	session := connectAutomationMCP(t, service)
	document := delayDocument(
		`{"id":"route","kind":"if","conditions":{"id":"manual","kind":"not","child":{"id":"tick-match","kind":"trigger","trigger_ids":["tick"]}},"then":[{"id":"select","kind":"choose","branches":[{"id":"manual-arm","conditions":{"id":"manual","kind":"not","child":{"id":"tick-match","kind":"trigger","trigger_ids":["tick"]}},"steps":[{"id":"nested-wait","kind":"delay","duration_ms":{"literal":1}}]}]}]},{"id":"last-wait","kind":"delay","duration_ms":{"literal":2}}`,
	)
	created := exactToolBody(
		t,
		callAutomationTool(t, session, "create_automation", json.RawMessage(`{"definition":`+document+`}`)),
	)
	id := created["automation_id"].(string)
	replaced := exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"replace_automation",
			json.RawMessage(fmt.Sprintf(`{"automation_id":%q,"expected_revision":1,"definition":%s}`, id, document)),
		),
	)
	if replaced["revision"] != json.Number("2") {
		t.Fatalf("receipt = %v", replaced)
	}
	read := exactToolBody(t, callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": id}))
	if canonicalJSON(t, read["definition"]) != canonicalJSON(t, exactJSONObject(t, document)) {
		t.Fatalf("recursive durations lost: %v", read)
	}
	started := exactToolBody(
		t,
		callAutomationTool(t, session, "start_automation_run", map[string]any{"automation_id": id}),
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
	if canonicalJSON(t, run["cause"]) != `{"kind":"manual"}` || run["revision"] != json.Number("2") ||
		canonicalJSON(t, run["snapshot"]) != canonicalJSON(t, exactJSONObject(t, document)) {
		t.Fatalf("provenance/snapshot = %v", run)
	}
	assertCompletedDelayOnlyRun(t, run, stub)
	if canonicalJSON(t, run["notifications"]) != "[]" {
		t.Fatalf("empty notifications = %v", run)
	}
	page := exactToolBody(
		t,
		callAutomationTool(t, session, "list_automation_history", map[string]any{"automation_id": id}),
	)
	item := page["items"].([]any)[0].(map[string]any)
	assertMembers(t, item, "entry_id", "automation_id", "kind", "revision", "recorded_at", "source", "status")
	if item["source"] != "manual" || item["status"] != "succeeded" {
		t.Fatalf("summary = %v", item)
	}
}

func TestDelayBusyAndImmutableSnapshotHistory(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	t.Cleanup(service.StopAdmission)
	session := connectAutomationMCP(t, service)
	document := delayDocument(`{"id":"wait","kind":"delay","duration_ms":{"literal":86400000}}`)
	created := exactToolBody(
		t,
		callAutomationTool(t, session, "create_automation", json.RawMessage(`{"definition":`+document+`}`)),
	)
	id := created["automation_id"].(string)
	started := exactToolBody(
		t,
		callAutomationTool(t, session, "start_automation_run", map[string]any{"automation_id": id}),
	)
	runID := started["run_id"].(string)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	waitForDelayEvidence(ctx, t, service, id, runID)
	busy := callAutomationTool(t, session, "start_automation_run", map[string]any{"automation_id": id})
	if !busy.IsError || decodeStructuredInto[map[string]any](t, busy)["failure_code"] != "automation_busy" {
		t.Fatalf("busy = %+v", busy)
	}
	replacement := strings.Replace(document, "86400000", "1", 1)
	exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"replace_automation",
			json.RawMessage(fmt.Sprintf(`{"automation_id":%q,"expected_revision":1,"definition":%s}`, id, replacement)),
		),
	)
	for _, status := range []string{"running", "interrupted"} {
		run := exactToolBody(
			t,
			callAutomationTool(
				t,
				session,
				"get_automation_history_entry",
				map[string]any{"automation_id": id, "entry_id": runID},
			),
		)
		if run["status"] != status ||
			canonicalJSON(t, run["snapshot"]) != canonicalJSON(t, exactJSONObject(t, document)) {
			t.Fatalf("active snapshot changed: %v", run)
		}
		delay := run["delays"].([]any)[0].(map[string]any)
		assertDelayMetadata(t, delay, "wait", 0, 86400000, status)
		if status == "interrupted" && delay["failure_code"] != "core_stopping" {
			t.Fatalf("interruption = %v", delay)
		}
		service.StopAdmission()
		if err := service.Drain(ctx); err != nil {
			t.Fatal(err)
		}
	}
	page := exactToolBody(
		t,
		callAutomationTool(t, session, "list_automation_history", map[string]any{"automation_id": id}),
	)
	if items := page["items"].([]any); len(items) != 1 || items[0].(map[string]any)["kind"] != "run" {
		t.Fatalf("manual busy created Skip: %v", page)
	}
}
