//go:build integration

package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// A13 requires exact operands and parameters at both write and read boundaries.
func TestBranchingHTTPExactRoundTripAndEvidence(t *testing.T) {
	t.Parallel()
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	stub := newAPIDevices()
	stub.setEntityStateSnapshot(apiStateSnapshot(t, entity, branchExactNumber))
	router, _, service := newAutomationHTTP(t, stub)
	session := newHTTPScenario(t, service)
	document := nestedBranchDocument(t, entity)
	created := callHTTPScenario(t, session, "create-automation", json.RawMessage(`{"definition":`+document+`}`))
	body := exactHTTPScenarioBody(t, created)
	assertNestedExactDefinition(t, body)
	id := body["id"].(string)
	path := "/v1/automations/" + id
	response := performJSON(router, http.MethodGet, path, "")
	assertNestedExactDefinition(t, exactJSONObject(t, response.Body.String()))
	assertNestedExactDefinition(
		t,
		exactHTTPScenarioBody(t, callHTTPScenario(t, session, "get-automation", map[string]any{"automation_id": id})),
	)
	collection := exactHTTPScenarioBody(t, callHTTPScenario(t, session, "list-automations", map[string]any{}))
	assertNestedExactDefinition(t, collection["items"].([]any)[0].(map[string]any))

	response = performJSON(router, http.MethodPost, "/v1/automations", document)
	if response.Code != http.StatusCreated {
		t.Fatalf("HTTP create = %d: %s", response.Code, response.Body.String())
	}
	assertNestedExactDefinition(t, exactJSONObject(t, response.Body.String()))
	httpID := decodeAutomation(t, response).ID
	response = performJSON(
		router,
		http.MethodPut,
		"/v1/automations/"+httpID,
		`{"expected_revision":1,"definition":`+document+`}`,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP replace = %d: %s", response.Code, response.Body.String())
	}
	assertNestedExactDefinition(t, exactJSONObject(t, response.Body.String()))
	replaced := callHTTPScenario(
		t,
		session,
		"replace-automation",
		json.RawMessage(fmt.Sprintf(`{"automation_id":%q,"expected_revision":1,"definition":%s}`, id, document)),
	)
	assertNestedExactDefinition(t, exactHTTPScenarioBody(t, replaced))
	started := callHTTPScenario(
		t,
		session,
		"start-automation-run",
		map[string]any{"automation_id": id, "bypass_conditions": true},
	)
	run := exactHTTPScenarioBody(t, started)
	assertNestedExactDefinition(t, map[string]any{"definition": run["snapshot"]})
	runID := run["id"].(string)
	waitForAPI(t, service, id, runID)
	entry := callHTTPScenario(
		t,
		session,
		"get-automation-history-entry",
		map[string]any{"automation_id": id, "entry_id": runID},
	)
	response = performJSON(router, http.MethodGet, path+"/history/"+runID, "")
	exactEntry := exactHTTPScenarioBody(t, entry)
	if canonicalJSON(t, exactEntry) != canonicalJSON(t, exactJSONObject(t, response.Body.String())) {
		t.Fatalf("HTTP scenario/read history mismatch: %s / %v", response.Body.String(), exactEntry)
	}
	run = exactEntry["run"].(map[string]any)
	decisions := contractBranchExecutions(t, run["executions"].([]any))
	if len(decisions) != 2 || run["status"] != "succeeded" {
		t.Fatalf("branch history = %v", run)
	}
	route := decisions[0]
	if route.execution["step_id"] != "route" || route.execution["kind"] != "choose" ||
		route.evidence["outcome"] != "branch" || route.evidence["selected_branch_id"] != "manual" {
		t.Fatalf("selection = %v", route)
	}
	evaluations := route.evidence["evaluations"].([]any)
	for _, evaluation := range evaluations {
		nodes := evaluation.(map[string]any)["evaluation"].(map[string]any)["nodes"].([]any)
		trigger := nodes[0].(map[string]any)
		if trigger["kind"] != "trigger" || canonicalJSON(t, trigger["matched_trigger_ids"]) != "[]" {
			t.Fatalf("false trigger evidence = %v", trigger)
		}
	}
	level := decisions[1]
	if level.execution["step_id"] != "level" || level.evidence["outcome"] != "then" {
		t.Fatalf("nested selection = %v", level)
	}
	stateEvidence := level.evidence["evaluations"].([]any)[0].(map[string]any)["evaluation"].(map[string]any)["nodes"].([]any)[0].(map[string]any)
	if stateEvidence["selected_value"] != json.Number(branchExactNumber) {
		t.Fatalf("rounded history State: %v", stateEvidence)
	}

	// Flat definitions retain always-present empty decisions in HTTP operations.
	flat := performJSON(router, http.MethodPost, "/v1/automations", definitionDocument(t, 1))
	flatID := decodeAutomation(t, flat).ID
	flatRun := performJSON(router, http.MethodPost, "/v1/automations/"+flatID+"/runs", "{}")
	flatBody := exactJSONObject(t, flatRun.Body.String())
	for _, legacy := range []string{"steps", "branch_decisions", "delays", "notifications", "functions"} {
		if _, exists := flatBody[legacy]; exists {
			t.Fatalf("run receipt exposes legacy evidence field %q: %v", legacy, flatBody)
		}
	}
	waitForAPI(t, service, flatID, flatBody["id"].(string))
	flatEntry := exactHTTPScenarioBody(
		t,
		callHTTPScenario(
			t,
			session,
			"get-automation-history-entry",
			map[string]any{"automation_id": flatID, "entry_id": flatBody["id"]},
		),
	)
	flatHistory := flatEntry["run"].(map[string]any)
	if len(contractBranchExecutions(t, flatHistory["executions"].([]any))) != 0 {
		t.Fatalf("flat Run has branch executions = %v", flatEntry)
	}
}

// Automatic history must expose nonempty Trigger intersections without State fields.
func TestBranchingTriggeredEvidenceHTTP(t *testing.T) {
	t.Parallel()
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	document := strings.Replace(nestedBranchDocument(t, entity),
		`{"id":"warm","kind":"cron","expression":"* * * * *"}`,
		fmt.Sprintf(`{"id":"warm","kind":"observation","entity_id":%q,"dispositions":["applied"]}`, entity), 1)
	response := performJSON(router, http.MethodPost, "/v1/automations", document)
	if response.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", response.Code, response.Body.String())
	}
	id := decodeAutomation(t, response).ID
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := service.ReceiveDeviceFact(t.Context(), automations.ObservationFact{
		FactID: factID, ObservationID: observationID, EntityID: entity,
		Disposition: devices.DispositionApplied, Value: devices.Value("true"), EmittedAt: time.Now().UTC(),
	})
	if err != nil || outcome.StartedRuns != 1 {
		t.Fatalf("admission = %+v, %v", outcome, err)
	}
	page, err := service.ListHistory(
		t.Context(),
		automations.ListHistoryParams{AutomationID: automations.AutomationID(id), Limit: 50},
	)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("history = %+v, %v", page, err)
	}
	runID := page.Items[0].ID
	waitForAPI(t, service, id, runID)
	session := newHTTPScenario(t, service)
	entry := exactHTTPScenarioBody(
		t,
		callHTTPScenario(
			t,
			session,
			"get-automation-history-entry",
			map[string]any{"automation_id": id, "entry_id": runID},
		),
	)
	response = performJSON(router, http.MethodGet, "/v1/automations/"+id+"/history/"+runID, "")
	if canonicalJSON(t, entry) != canonicalJSON(t, exactJSONObject(t, response.Body.String())) {
		t.Fatal("automatic HTTP scenario/read evidence differs")
	}
	decisions := contractBranchExecutions(t, entry["run"].(map[string]any)["executions"].([]any))
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
		t.Fatalf("trigger evidence contains missing IDs or State fields: %v", node)
	}
}

// Invalid recursive shapes must leave definitions and revisions unchanged.
//
//nolint:paralleltest,tparallel // Cases share one revisioned definition and test rejection without writes.
func TestBranchingHTTPRejectCreateAndReplace(t *testing.T) {
	t.Parallel()
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := newHTTPScenario(t, service)
	document := nestedBranchDocument(t, entity)
	created := performJSON(router, http.MethodPost, "/v1/automations", document)
	id := decodeAutomation(t, created).ID
	invalid := map[string]string{
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
	for name, bad := range invalid {
		t.Run(name, func(t *testing.T) {
			for _, method := range []string{http.MethodPost, http.MethodPut} {
				path, body, tool := "/v1/automations", bad, "create-automation"
				arguments := json.RawMessage(`{"definition":` + bad + `}`)
				if method == http.MethodPut {
					path += "/" + id
					body = `{"expected_revision":1,"definition":` + bad + `}`
					tool = "replace-automation"
					arguments = json.RawMessage(
						fmt.Sprintf(`{"automation_id":%q,"expected_revision":1,"definition":%s}`, id, bad),
					)
				}
				response := performJSON(router, method, path, body)
				if response.Code != http.StatusBadRequest {
					t.Fatalf("%s accepted invalid shape: %d %s", method, response.Code, response.Body.String())
				}
				if result := callHTTPScenario(t, session, tool, arguments); !result.Failed {
					t.Fatalf("%s accepted invalid shape", tool)
				}
			}
		})
	}
	collection, err := service.ListAutomations(t.Context(), automations.ListAutomationsParams{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Items) != 1 || collection.Items[0].Revision != 1 {
		t.Fatalf("rejections wrote definitions: %v", collection)
	}
}

// Compile the actual advertised schema locations, so dangling recursive refs fail.
func TestBranchingPublishedSchemasResolveAndValidateNestedDefinitions(t *testing.T) {
	t.Parallel()
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	openapi := pageObject(t, restJSON(t, router, "/openapi.json"))
	runSchema := compilePublishedSchema(
		t,
		openapi,
		"https://hearth.invalid/openapi#/components/schemas/AutomationRunBody",
	)
	if runSchema.Ref == nil || !slices.Contains(runSchema.Ref.Required, "executions") {
		t.Fatal("Run schema does not require executions")
	}
	assertPublishedBranchSchema(
		t,
		openapi,
		"https://hearth.test/openapi.json",
		"#/components/schemas/AutomationBody/$defs/definition",
		nestedBranchDocument(t, entity),
	)
	session := newHTTPScenario(t, service)
	tools, err := session.contracts()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Operations {
		if tool.Name != "create-automation" && tool.Name != "replace-automation" {
			continue
		}
		definition := inputSchemaDefinition(t, tool.Name, tool)
		assertPublishedBranchSchema(
			t,
			definition,
			"https://hearth.test/"+tool.Name,
			"",
			nestedBranchDocument(t, entity),
		)
	}
}

type contractBranchExecution struct {
	execution map[string]any
	evidence  map[string]any
}

func contractBranchExecutions(t *testing.T, executions []any) []contractBranchExecution {
	t.Helper()
	var branches []contractBranchExecution
	for _, item := range executions {
		execution := item.(map[string]any)
		kind := execution["kind"]
		if kind != "if" && kind != "choose" {
			continue
		}
		evidence, ok := execution["evidence"].(map[string]any)
		if !ok {
			t.Fatalf("branch execution has no evidence object: %v", execution)
		}
		branches = append(branches, contractBranchExecution{execution: execution, evidence: evidence})
	}
	return branches
}
