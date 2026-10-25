//go:build integration

package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Runtime and discovery must reject the same duration/family shapes without writing.
// Global identity and tree bounds remain enforced by the canonical decoder.
//
//nolint:paralleltest,tparallel // Cases share a revisioned baseline to prove rejected writes leave it unchanged.
func TestDelayHTTPInputAndDiscoveryContract(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := newHTTPScenario(t, service)
	tools := delayOperationContracts(t, session)
	openapi := pageObject(t, restJSON(t, router, "/openapi.json"))
	schemas := []*jsonschema.Schema{
		compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationDefinition"),
		compileDelayContractSchema(t, inputSchemaDefinition(t, "create-automation", tools["create-automation"]), ""),
		compileDelayContractSchema(t, inputSchemaDefinition(t, "replace-automation", tools["replace-automation"]), ""),
	}
	valid := delayDocument(`{"id":"wait","kind":"delay","duration_ms":{"literal":1}}`)
	created := performJSON(router, http.MethodPost, "/v1/automations", valid)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", created.Code, created.Body.String())
	}
	id := decodeAutomation(t, created).ID
	for _, duration := range []string{"1", "86400000"} {
		document := delayDocument(`{"id":"wait","kind":"delay","duration_ms":{"literal":` + duration + `}}`)
		for _, schema := range schemas {
			if err := schema.Validate(exactJSONObject(t, document)); err != nil {
				t.Fatalf("discovery rejects valid duration %s: %v", duration, err)
			}
		}
		response := performJSON(router, http.MethodPost, "/v1/automations", document)
		if response.Code != http.StatusCreated {
			t.Fatalf("HTTP rejects valid duration %s: %s", duration, response.Body.String())
		}
		exactHTTPScenarioBody(
			t,
			callHTTPScenario(t, session, "create-automation", json.RawMessage(`{"definition":`+document+`}`)),
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
	for index := range 33 {
		tooMany = append(tooMany, fmt.Sprintf(`{"id":"wait-%d","kind":"delay","duration_ms":{"literal":1}}`, index))
	}
	cases["sequence bound"] = strings.Join(tooMany, ",")
	for name, steps := range cases {
		t.Run(name, func(t *testing.T) {
			bad := delayDocument(steps)
			assertDelayDiscoveryRejected(t, schemas, bad)
			assertDelayWriteRejected(t, router, session, id, bad)
		})
	}
	// These are semantic decoder constraints, not claims about JSON Schema.
	duplicate := delayDocument(
		`{"id":"wait","kind":"delay","duration_ms":{"literal":1}},{"id":"wait","kind":"delay","duration_ms":{"literal":2}}`,
	)
	assertDelayWriteRejected(t, router, session, id, duplicate)
	for _, bad := range delaySemanticInvalidDocuments() {
		assertDelayWriteRejected(t, router, session, id, bad)
	}
	baseline := exactJSONObject(t, performJSON(router, http.MethodGet, "/v1/automations/"+id, "").Body.String())
	if baseline["revision"] != json.Number("1") ||
		canonicalJSON(t, baseline["definition"]) != canonicalJSON(t, exactJSONObject(t, valid)) {
		t.Fatalf("rejected replacements changed baseline: %v", baseline)
	}
	collection, err := service.ListAutomations(t.Context(), automations.ListAutomationsParams{Limit: 50})
	if err != nil || len(collection.Items) != 5 {
		t.Fatalf("rejected creates wrote definitions: %+v, %v", collection, err)
	}
}

// HTTP operations author and run recursive delay-only definitions and return
// schema-valid evidence with independently calculated snapshot metadata.
func TestDelayHTTPRoundTripAndCompletedHistory(t *testing.T) {
	t.Parallel()
	for _, transport := range []string{"HTTP"} {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			stub := newAPIDevices()
			router, _, service := newAutomationHTTP(t, stub)
			session := newHTTPScenario(t, service)
			tools := delayOperationContracts(t, session)
			openapi := pageObject(t, restJSON(t, router, "/openapi.json"))
			document := delayDocument(
				`{"id":"route","kind":"if","conditions":{"id":"manual","kind":"not","child":{"id":"tick-match","kind":"trigger","trigger_ids":["tick"]}},"then":[{"id":"select","kind":"choose","branches":[{"id":"manual-arm","conditions":{"id":"manual","kind":"not","child":{"id":"tick-match","kind":"trigger","trigger_ids":["tick"]}},"steps":[{"id":"nested-wait","kind":"delay","duration_ms":{"literal":1}}]}]}]},{"id":"last-wait","kind":"delay","duration_ms":{"literal":2}}`,
			)
			body := writeDelayDefinition(t, router, session, transport, "", document)
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationBody"),
				body,
			)
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, tools["create-automation"].ResponseSchema, ""),
				body,
			)
			if canonicalJSON(t, body["definition"]) != canonicalJSON(t, exactJSONObject(t, document)) {
				t.Fatalf("recursive durations lost: %v", body)
			}
			id := body["id"].(string)
			path := "/v1/automations/" + id
			body = writeDelayDefinition(t, router, session, transport, id, document)
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, tools["replace-automation"].ResponseSchema, ""),
				body,
			)
			read := exactHTTPScenarioBody(
				t,
				callHTTPScenario(t, session, "get-automation", map[string]any{"automation_id": id}),
			)
			assertDelayHTTPMatches(t, router, path, read)
			assertDelaySchemaValid(t, compileDelayContractSchema(t, tools["get-automation"].ResponseSchema, ""), read)
			if canonicalJSON(t, read) != canonicalJSON(t, body) {
				t.Fatal("replacement/read differ")
			}
			run := startDelayRun(t, router, session, transport, id)
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationRunBody"),
				run,
			)
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, tools["start-automation-run"].ResponseSchema, ""),
				run,
			)
			if canonicalJSON(t, run["delays"]) != "[]" || canonicalJSON(t, run["steps"]) != "[]" {
				t.Fatalf("admission arrays = %v", run)
			}
			if canonicalJSON(t, run["cause"]) != `{"kind":"manual"}` ||
				run["revision"] != json.Number("2") ||
				canonicalJSON(t, run["snapshot"]) != canonicalJSON(t, exactJSONObject(t, document)) {
				t.Fatalf("admission provenance/snapshot = %v", run)
			}
			runID := run["id"].(string)
			waitForAPI(t, service, id, runID)
			entry := exactHTTPScenarioBody(
				t,
				callHTTPScenario(
					t,
					session,
					"get-automation-history-entry",
					map[string]any{"automation_id": id, "entry_id": runID},
				),
			)
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, tools["get-automation-history-entry"].ResponseSchema, ""),
				entry,
			)
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationHistoryEntryBody"),
				entry,
			)
			assertDelayHTTPMatches(t, router, path+"/history/"+runID, entry)
			assertCompletedDelayOnlyRun(t, entry["run"].(map[string]any), stub)
			page := exactHTTPScenarioBody(
				t,
				callHTTPScenario(t, session, "list-automation-history", map[string]any{"automation_id": id}),
			)
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, tools["list-automation-history"].ResponseSchema, ""),
				page,
			)
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationHistoryCollectionBody"),
				page,
			)
			assertDelayHTTPMatches(t, router, path+"/history", page)
		})
	}
}

// A real active wait keeps manual conflicts out of history and preserves original
// snapshot duration after replacement, in both running and interrupted reads.
func TestDelayBusyHTTPAndSnapshotHistory(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	t.Cleanup(service.StopAdmission)
	session := newHTTPScenario(t, service)
	tools := delayOperationContracts(t, session)
	openapi := pageObject(t, restJSON(t, router, "/openapi.json"))
	document := delayDocument(`{"id":"wait","kind":"delay","duration_ms":{"literal":86400000}}`)
	created := performJSON(router, http.MethodPost, "/v1/automations", document)
	id := decodeAutomation(t, created).ID
	path := "/v1/automations/" + id
	started := exactHTTPScenarioBody(
		t,
		callHTTPScenario(t, session, "start-automation-run", map[string]any{"automation_id": id}),
	)
	runID := started["id"].(string)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	waitForDelayEvidence(ctx, t, service, id, runID)
	busy := performJSON(router, http.MethodPost, path+"/runs", "{}")
	if busy.Code != http.StatusConflict {
		t.Fatalf("busy = %d: %s", busy.Code, busy.Body.String())
	}
	if result := callHTTPScenario(
		t,
		session,
		"start-automation-run",
		map[string]any{"automation_id": id},
	); !result.Failed {
		t.Fatal("HTTP admitted busy Run")
	}
	replacement := strings.Replace(document, "86400000", "1", 1)
	response := performJSON(router, http.MethodPut, path, `{"expected_revision":1,"definition":`+replacement+`}`)
	if response.Code != http.StatusOK {
		t.Fatalf("replace = %d: %s", response.Code, response.Body.String())
	}
	for _, status := range []string{"running", "interrupted"} {
		entry := exactHTTPScenarioBody(
			t,
			callHTTPScenario(
				t,
				session,
				"get-automation-history-entry",
				map[string]any{"automation_id": id, "entry_id": runID},
			),
		)
		assertDelaySchemaValid(
			t,
			compileDelayContractSchema(t, tools["get-automation-history-entry"].ResponseSchema, ""),
			entry,
		)
		assertDelaySchemaValid(
			t,
			compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationHistoryEntryBody"),
			entry,
		)
		if canonicalJSON(
			t,
			entry,
		) != canonicalJSON(
			t,
			exactJSONObject(t, performJSON(router, http.MethodGet, path+"/history/"+runID, "").Body.String()),
		) {
			t.Fatal("active/interrupted HTTP scenario/read history differs")
		}
		run := entry["run"].(map[string]any)
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
	page := exactHTTPScenarioBody(
		t,
		callHTTPScenario(t, session, "list-automation-history", map[string]any{"automation_id": id}),
	)
	if items := page["items"].([]any); len(items) != 1 || items[0].(map[string]any)["kind"] != "run" {
		t.Fatalf("manual busy created Skip: %v", page)
	}
}
