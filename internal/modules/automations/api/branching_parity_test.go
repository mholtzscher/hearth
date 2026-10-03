package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const branchExactNumber = "9007199254740993"

func nestedBranchDocument(t *testing.T, entity devices.EntityID) string {
	t.Helper()
	command := fmt.Sprintf(
		`{"id":"selected","entity_id":%q,"operation":"set","parameters":{"value":%s}}`,
		entity,
		branchExactNumber,
	)
	return fmt.Sprintf(
		`{"name":"Nested","enabled":true,"triggers":[{"id":"warm","kind":"cron","expression":"* * * * *"}],"steps":[
		{"id":"route","kind":"choose","branches":[
			{"id":"automatic","conditions":{"id":"matched","kind":"trigger","trigger_ids":["warm"]},"steps":[{"id":"unselected","entity_id":%q,"operation":"set","parameters":{}}]},
			{"id":"manual","conditions":{"id":"not-matched","kind":"not","child":{"id":"matched","kind":"trigger","trigger_ids":["warm"]}},"steps":[
				{"id":"level","kind":"if","conditions":{"id":"exact","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"eq","operand":%s},"then":[%s]}]}
		]}]}`,
		entity,
		entity,
		branchExactNumber,
		command,
	)
}

// Decode the protocol's JSON text, not the SDK client's float64 StructuredContent.
func exactToolBody(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatal(toolErrorText(t, result))
	}
	if len(result.Content) != 1 {
		t.Fatalf("unexpected result content: %v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("result content is %T", result.Content[0])
	}
	return exactJSONObject(t, text.Text)
}

func exactJSONObject(t *testing.T, document string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewBufferString(document))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		t.Fatal(err)
	}
	delete(object, "$schema")
	return object
}

func assertNestedExactDefinition(t *testing.T, body map[string]any) {
	t.Helper()
	definition := body["definition"].(map[string]any)
	route := definition["steps"].([]any)[0].(map[string]any)
	branches := route["branches"].([]any)
	level := branches[1].(map[string]any)["steps"].([]any)[0].(map[string]any)
	condition := level["conditions"].(map[string]any)
	command := level["then"].([]any)[0].(map[string]any)
	parameters := command["parameters"].(map[string]any)
	if condition["operand"] != json.Number(branchExactNumber) || parameters["value"] != json.Number(branchExactNumber) {
		t.Fatalf("rounded operand/parameters: %v / %v", condition, parameters)
	}
	for _, field := range []string{"kind", "conditions", "then", "branches"} {
		if _, present := command[field]; present {
			t.Fatalf("command contains %s: %v", field, command)
		}
	}
	if _, present := level["else"]; present {
		t.Fatal("omitted else was emitted")
	}
	if _, present := route["default"]; present {
		t.Fatal("omitted default was emitted")
	}
	if _, present := route["entity_id"]; present {
		t.Fatal("branch contains command fields")
	}
}

// A13 requires exact operands and parameters at both write and read boundaries.
func TestBranchingHTTPMCPExactRoundTripAndEvidence(t *testing.T) {
	t.Parallel()
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	stub := newAPIDevices()
	stub.setEntityStateSnapshot(apiStateSnapshot(t, entity, branchExactNumber))
	router, _, service := newAutomationHTTP(t, stub)
	session := connectAutomationMCP(t, service)
	document := nestedBranchDocument(t, entity)
	created := callAutomationTool(t, session, "create_automation", json.RawMessage(`{"definition":`+document+`}`))
	body := exactToolBody(t, created)
	assertNestedExactDefinition(t, body)
	id := body["id"].(string)
	path := "/v1/automations/" + id
	response := performJSON(router, http.MethodGet, path, "")
	assertNestedExactDefinition(t, exactJSONObject(t, response.Body.String()))
	assertNestedExactDefinition(
		t,
		exactToolBody(t, callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": id})),
	)
	collection := exactToolBody(t, callAutomationTool(t, session, "list_automations", map[string]any{}))
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
	replaced := callAutomationTool(
		t,
		session,
		"replace_automation",
		json.RawMessage(fmt.Sprintf(`{"automation_id":%q,"expected_revision":1,"definition":%s}`, id, document)),
	)
	assertNestedExactDefinition(t, exactToolBody(t, replaced))
	started := callAutomationTool(
		t,
		session,
		"start_automation_run",
		map[string]any{"automation_id": id, "bypass_conditions": true},
	)
	run := exactToolBody(t, started)
	assertNestedExactDefinition(t, map[string]any{"definition": run["snapshot"]})
	runID := run["id"].(string)
	waitForAPI(t, service, id, runID)
	entry := callAutomationTool(
		t,
		session,
		"get_automation_history_entry",
		map[string]any{"automation_id": id, "entry_id": runID},
	)
	response = performJSON(router, http.MethodGet, path+"/history/"+runID, "")
	exactEntry := exactToolBody(t, entry)
	if canonicalJSON(t, exactEntry) != canonicalJSON(t, exactJSONObject(t, response.Body.String())) {
		t.Fatalf("HTTP/MCP history mismatch: %s / %v", response.Body.String(), exactEntry)
	}
	run = exactEntry["run"].(map[string]any)
	decisions := run["branch_decisions"].([]any)
	if len(decisions) != 2 || run["status"] != "succeeded" {
		t.Fatalf("branch history = %v", run)
	}
	route := decisions[0].(map[string]any)
	if route["step_id"] != "route" || route["outcome"] != "branch" || route["selected_branch_id"] != "manual" {
		t.Fatalf("selection = %v", route)
	}
	evaluations := route["evaluations"].([]any)
	for _, evaluation := range evaluations {
		nodes := evaluation.(map[string]any)["evaluation"].(map[string]any)["nodes"].([]any)
		trigger := nodes[0].(map[string]any)["trigger"].(map[string]any)
		if canonicalJSON(t, trigger["matched_trigger_ids"]) != "[]" {
			t.Fatalf("false trigger evidence = %v", trigger)
		}
	}
	level := decisions[1].(map[string]any)
	if level["outcome"] != "then" {
		t.Fatalf("nested selection = %v", level)
	}
	stateEvidence := level["evaluations"].([]any)[0].(map[string]any)["evaluation"].(map[string]any)["nodes"].([]any)[0].(map[string]any)
	if stateEvidence["selected_value"] != json.Number(branchExactNumber) {
		t.Fatalf("rounded history State: %v", stateEvidence)
	}

	// Flat definitions retain always-present empty decisions in both transports.
	flat := performJSON(router, http.MethodPost, "/v1/automations", definitionDocument(t, 1))
	flatID := decodeAutomation(t, flat).ID
	flatRun := performJSON(router, http.MethodPost, "/v1/automations/"+flatID+"/runs", "{}")
	flatBody := exactJSONObject(t, flatRun.Body.String())
	if canonicalJSON(t, flatBody["branch_decisions"]) != "[]" {
		t.Fatalf("old decisions = %v", flatBody)
	}
	waitForAPI(t, service, flatID, flatBody["id"].(string))
	flatEntry := exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"get_automation_history_entry",
			map[string]any{"automation_id": flatID, "entry_id": flatBody["id"]},
		),
	)
	if canonicalJSON(t, flatEntry["run"].(map[string]any)["branch_decisions"]) != "[]" {
		t.Fatalf("MCP old decisions = %v", flatEntry)
	}
}

// Automatic history must expose nonempty Trigger intersections without State fields.
func TestBranchingTriggeredEvidenceHTTPMCP(t *testing.T) {
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
	outcome, err := service.ReceiveDeviceFact(t.Context(), automations.DeviceFact{
		Family: automations.DeviceFactObservation,
		Observation: &automations.ObservationFact{
			FactID: factID, ObservationID: observationID, EntityID: entity,
			Disposition: devices.DispositionApplied, Value: devices.Value("true"), EmittedAt: time.Now().UTC(),
		},
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
	session := connectAutomationMCP(t, service)
	entry := exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"get_automation_history_entry",
			map[string]any{"automation_id": id, "entry_id": runID},
		),
	)
	response = performJSON(router, http.MethodGet, "/v1/automations/"+id+"/history/"+runID, "")
	if canonicalJSON(t, entry) != canonicalJSON(t, exactJSONObject(t, response.Body.String())) {
		t.Fatal("automatic HTTP/MCP evidence differs")
	}
	decisions := entry["run"].(map[string]any)["branch_decisions"].([]any)
	if len(decisions) != 1 {
		t.Fatalf("automatic decisions = %v", decisions)
	}
	decision := decisions[0].(map[string]any)
	if decision["selected_branch_id"] != "automatic" {
		t.Fatalf("automatic selection = %v", decision)
	}
	evaluations := decision["evaluations"].([]any)
	if len(evaluations) != 1 {
		t.Fatalf("automatic evaluations = %v", evaluations)
	}
	node := evaluations[0].(map[string]any)["evaluation"].(map[string]any)["nodes"].([]any)[0].(map[string]any)
	if node["result"] != "true" || canonicalJSON(t, node["trigger"]) != `{"matched_trigger_ids":["warm"]}` ||
		len(node) != 3 {
		t.Fatalf("trigger evidence contains missing IDs or State fields: %v", node)
	}
}

// Invalid recursive shapes must leave definitions and revisions unchanged.
//
//nolint:paralleltest,tparallel // Cases share one revisioned definition and test rejection without writes.
func TestBranchingHTTPMCPRejectCreateAndReplace(t *testing.T) {
	t.Parallel()
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	document := nestedBranchDocument(t, entity)
	created := performJSON(router, http.MethodPost, "/v1/automations", document)
	id := decodeAutomation(t, created).ID
	invalid := map[string]string{
		"command kind":     strings.Replace(document, `"id":"selected"`, `"id":"selected","kind":"command"`, 1),
		"mixed fields":     strings.Replace(document, `"kind":"if"`, `"kind":"if","operation":"set"`, 1),
		"unknown":          strings.Replace(document, `"kind":"if"`, `"kind":"if","surprise":true`, 1),
		"null":             strings.Replace(document, `"kind":"if"`, `"kind":"if","else":null`, 1),
		"empty":            strings.Replace(document, `"kind":"if"`, `"kind":"if","else":[]`, 1),
		"bad trigger":      strings.ReplaceAll(document, `["warm"]`, `["missing"]`),
		"duplicate step":   strings.Replace(document, `"id":"selected"`, `"id":"unselected"`, 1),
		"duplicate branch": strings.Replace(document, `"id":"manual"`, `"id":"automatic"`, 1),
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
				path, body, tool := "/v1/automations", bad, "create_automation"
				arguments := json.RawMessage(`{"definition":` + bad + `}`)
				if method == http.MethodPut {
					path += "/" + id
					body = `{"expected_revision":1,"definition":` + bad + `}`
					tool = "replace_automation"
					arguments = json.RawMessage(
						fmt.Sprintf(`{"automation_id":%q,"expected_revision":1,"definition":%s}`, id, bad),
					)
				}
				response := performJSON(router, method, path, body)
				if response.Code != http.StatusBadRequest {
					t.Fatalf("%s accepted invalid shape: %d %s", method, response.Code, response.Body.String())
				}
				if result := callAutomationTool(t, session, tool, arguments); !result.IsError {
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
	schemas := openapi["components"].(map[string]any)["schemas"].(map[string]any)
	admission := schemas["AutomationConditionBody"].(map[string]any)["properties"].(map[string]any)
	if containsOpenAPIValue(admission["kind"].(map[string]any)["enum"].([]any), "trigger") {
		t.Fatal("admission DTO schema includes branch-only Trigger Conditions")
	}
	runSchema := schemas["AutomationRunBody"].(map[string]any)
	if !schemaStringSet(t, "Run required fields", runSchema["required"])["branch_decisions"] {
		t.Fatal("Run schema does not require branch_decisions")
	}
	assertPublishedBranchSchema(
		t,
		openapi,
		"https://hearth.test/openapi.json",
		"#/components/schemas/AutomationDefinition",
		nestedBranchDocument(t, entity),
	)
	session := connectAutomationMCP(t, service)
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "create_automation" && tool.Name != "replace_automation" {
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

func assertPublishedBranchSchema(t *testing.T, document map[string]any, uri, fragment, valid string) {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(uri, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(uri + fragment)
	if err != nil {
		t.Fatalf("published refs do not resolve: %v", err)
	}
	if err = schema.Validate(exactJSONObject(t, valid)); err != nil {
		t.Fatalf("published schema rejects nested definition: %v", err)
	}
	bad := strings.Replace(valid, `"kind":"if"`, `"kind":"if","else":[]`, 1)
	if err = schema.Validate(exactJSONObject(t, bad)); err == nil {
		t.Fatal("published schema accepts empty recursive arm")
	}
	bad = strings.Replace(
		valid,
		`"steps":[`,
		`"conditions":{"id":"invalid","kind":"trigger","trigger_ids":["warm"]},"steps":[`,
		1,
	)
	if err = schema.Validate(exactJSONObject(t, bad)); err == nil {
		t.Fatal("published admission schema accepts trigger leaf")
	}
}
