package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func delayDocument(steps string) string {
	return `{"name":"Wait","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[` + steps + `]}`
}

func compileDelayContractSchema(t *testing.T, document any, fragment string) *jsonschema.Schema {
	t.Helper()
	return compilePublishedSchema(t, document, "https://hearth.test/contract"+fragment)
}

func delayTools(t *testing.T, session *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := make(map[string]*mcp.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func assertDelaySchemaValid(t *testing.T, schema *jsonschema.Schema, body map[string]any) {
	t.Helper()
	if err := schema.Validate(body); err != nil {
		t.Fatalf("advertised output schema rejects handler result %v: %v", body, err)
	}
}

// Runtime and discovery must reject the same duration/family shapes without writing.
// Global identity and tree bounds remain enforced by the canonical decoder.
//
//nolint:paralleltest,tparallel // Cases share a revisioned baseline to prove rejected writes leave it unchanged.
func TestDelayHTTPMCPInputAndDiscoveryContract(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	tools := delayTools(t, session)
	openapi := pageObject(t, restJSON(t, router, "/openapi.json"))
	schemas := []*jsonschema.Schema{
		compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationDefinition"),
		compileDelayContractSchema(t, inputSchemaDefinition(t, "create_automation", tools["create_automation"]), ""),
		compileDelayContractSchema(t, inputSchemaDefinition(t, "replace_automation", tools["replace_automation"]), ""),
	}
	valid := delayDocument(`{"id":"wait","kind":"delay","duration_ms":1}`)
	created := performJSON(router, http.MethodPost, "/v1/automations", valid)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", created.Code, created.Body.String())
	}
	id := decodeAutomation(t, created).ID
	for _, duration := range []string{"1", "86400000"} {
		document := delayDocument(`{"id":"wait","kind":"delay","duration_ms":` + duration + `}`)
		for _, schema := range schemas {
			if err := schema.Validate(exactJSONObject(t, document)); err != nil {
				t.Fatalf("discovery rejects valid duration %s: %v", duration, err)
			}
		}
		response := performJSON(router, http.MethodPost, "/v1/automations", document)
		if response.Code != http.StatusCreated {
			t.Fatalf("HTTP rejects valid duration %s: %s", duration, response.Body.String())
		}
		exactToolBody(
			t,
			callAutomationTool(t, session, "create_automation", json.RawMessage(`{"definition":`+document+`}`)),
		)
	}
	cases := map[string]string{
		"missing":        `{"id":"wait","kind":"delay"}`,
		"mixed command":  `{"id":"wait","kind":"delay","duration_ms":1,"operation":"set"}`,
		"mixed branch":   `{"id":"wait","kind":"delay","duration_ms":1,"then":[]}`,
		"unknown":        `{"id":"wait","kind":"delay","duration_ms":1,"extra":true}`,
		"empty sequence": "",
	}
	for _, duration := range []string{"null", "0", "-1", "1.5", "86400001", "9223372036854775808", `"1"`} {
		cases[duration] = `{"id":"wait","kind":"delay","duration_ms":` + duration + `}`
	}
	var tooMany []string
	for index := range 33 {
		tooMany = append(tooMany, fmt.Sprintf(`{"id":"wait-%d","kind":"delay","duration_ms":1}`, index))
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
		`{"id":"wait","kind":"delay","duration_ms":1},{"id":"wait","kind":"delay","duration_ms":2}`,
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

func delaySemanticInvalidDocuments() []string {
	predicate := `{"id":"matched","kind":"trigger","trigger_ids":["tick"]}`
	deep := `{"id":"wait","kind":"delay","duration_ms":1}`
	for index := range 8 {
		deep = fmt.Sprintf(`{"id":"if-%d","kind":"if","conditions":%s,"then":[%s]}`, index, predicate, deep)
	}
	var arms []string
	for arm := range 3 {
		var children []string
		for child := range 21 {
			children = append(children, fmt.Sprintf(`{"id":"wait-%d-%d","kind":"delay","duration_ms":1}`, arm, child))
		}
		arms = append(
			arms,
			fmt.Sprintf(
				`{"id":"if-%d","kind":"if","conditions":%s,"then":[%s]}`,
				arm,
				predicate,
				strings.Join(children, ","),
			),
		)
	}
	return []string{delayDocument(deep), delayDocument(strings.Join(arms, ","))}
}

func assertDelayDiscoveryRejected(t *testing.T, schemas []*jsonschema.Schema, bad string) {
	t.Helper()
	for _, schema := range schemas {
		if err := schema.Validate(exactJSONObject(t, bad)); err == nil {
			t.Fatal("discovery accepts invalid definition")
		}
	}
}

func assertDelayWriteRejected(t *testing.T, router http.Handler, session *mcp.ClientSession, id, bad string) {
	t.Helper()
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
			t.Fatalf("%s accepted invalid definition: %d %s", method, response.Code, response.Body.String())
		}
		if result := callAutomationTool(t, session, tool, arguments); !result.IsError {
			t.Fatalf("%s accepted invalid definition", tool)
		}
	}
}

// One real service/SQLite flow crosses both write transports and reads completed
// recursive delay evidence. Execution matrices live with the service and store.
func TestDelayHTTPMCPRoundTripAndCompletedHistory(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	router, _, service := newAutomationHTTP(t, stub)
	session := connectAutomationMCP(t, service)
	tools := delayTools(t, session)
	openapi := pageObject(t, restJSON(t, router, "/openapi.json"))
	document := delayDocument(
		`{"id":"route","kind":"if","conditions":{"id":"manual","kind":"not","child":{"id":"tick-match","kind":"trigger","trigger_ids":["tick"]}},"then":[{"id":"select","kind":"choose","branches":[{"id":"manual-arm","conditions":{"id":"manual","kind":"not","child":{"id":"tick-match","kind":"trigger","trigger_ids":["tick"]}},"steps":[{"id":"nested-wait","kind":"delay","duration_ms":1}]}]}]},{"id":"last-wait","kind":"delay","duration_ms":2}`,
	)
	body := writeAutomationDefinition(t, router, session, "HTTP", "", document)
	assertDelaySchemaValid(
		t,
		compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationBody"),
		body,
	)
	assertDelaySchemaValid(t, compileDelayContractSchema(t, tools["create_automation"].OutputSchema, ""), body)
	if canonicalJSON(t, body["definition"]) != canonicalJSON(t, exactJSONObject(t, document)) {
		t.Fatalf("recursive durations lost: %v", body)
	}
	id := body["id"].(string)
	path := "/v1/automations/" + id
	body = writeAutomationDefinition(t, router, session, "MCP", id, document)
	assertDelaySchemaValid(t, compileDelayContractSchema(t, tools["replace_automation"].OutputSchema, ""), body)
	read := exactToolBody(
		t,
		callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": id}),
	)
	assertDelayHTTPMatches(t, router, path, read)
	assertDelaySchemaValid(t, compileDelayContractSchema(t, tools["get_automation"].OutputSchema, ""), read)
	if canonicalJSON(t, read) != canonicalJSON(t, body) {
		t.Fatal("replacement/read differ")
	}
	run := startAutomationRun(t, router, session, "MCP", id)
	assertDelaySchemaValid(
		t,
		compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationRunBody"),
		run,
	)
	assertDelaySchemaValid(
		t,
		compileDelayContractSchema(t, tools["start_automation_run"].OutputSchema, ""),
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
	entry := exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"get_automation_history_entry",
			map[string]any{"automation_id": id, "entry_id": runID},
		),
	)
	assertDelaySchemaValid(
		t,
		compileDelayContractSchema(t, tools["get_automation_history_entry"].OutputSchema, ""),
		entry,
	)
	assertDelaySchemaValid(
		t,
		compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationHistoryEntryBody"),
		entry,
	)
	assertDelayHTTPMatches(t, router, path+"/history/"+runID, entry)
	assertCompletedDelayOnlyRun(t, entry["run"].(map[string]any), stub)
	page := exactToolBody(
		t,
		callAutomationTool(t, session, "list_automation_history", map[string]any{"automation_id": id}),
	)
	assertDelaySchemaValid(
		t,
		compileDelayContractSchema(t, tools["list_automation_history"].OutputSchema, ""),
		page,
	)
	assertDelaySchemaValid(
		t,
		compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationHistoryCollectionBody"),
		page,
	)
	assertDelayHTTPMatches(t, router, path+"/history", page)
}

func assertCompletedDelayOnlyRun(t *testing.T, run map[string]any, stub *apiDevices) {
	t.Helper()
	if run["status"] != "succeeded" || canonicalJSON(t, run["steps"]) != "[]" || stub.executionCount() != 0 {
		t.Fatalf("delay-only execution = %v, Commands = %d", run, stub.executionCount())
	}
	if canonicalJSON(t, run["notifications"]) != "[]" {
		t.Fatalf("pre-notification definition must return notifications: []: %v", run)
	}
	delays := run["delays"].([]any)
	if len(delays) != 2 {
		t.Fatalf("reached delays = %v", delays)
	}
	for index, stepID := range []string{"nested-wait", "last-wait"} {
		assertDelayMetadata(t, delays[index].(map[string]any), stepID, index, int64(index+1), "completed")
	}
}

func assertDelayHTTPMatches(t *testing.T, router http.Handler, path string, body map[string]any) {
	t.Helper()
	response := performJSON(router, http.MethodGet, path, "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, response.Code, response.Body.String())
	}
	if canonicalJSON(t, body) != canonicalJSON(t, exactJSONObject(t, response.Body.String())) {
		t.Fatalf("HTTP/MCP %s differ: %s / %v", path, response.Body.String(), body)
	}
}

func assertDelayMetadata(t *testing.T, delay map[string]any, id string, position int, duration int64, status string) {
	t.Helper()
	started, err := time.Parse(time.RFC3339Nano, delay["started_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	due, err := time.Parse(time.RFC3339Nano, delay["due_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if delay["step_id"] != id || delay["position"] != json.Number(strconv.Itoa(position)) ||
		delay["duration_ms"] != json.Number(strconv.FormatInt(duration, 10)) ||
		delay["status"] != status ||
		!due.Equal(started.Add(time.Duration(duration)*time.Millisecond)) {
		t.Fatalf("incorrect snapshot-derived delay metadata: %v", delay)
	}
	_, completed := delay["completed_at"]
	_, failed := delay["failure_code"]
	if completed != (status != "running") || failed != (status == "interrupted") {
		t.Fatalf("terminal field presence = %v", delay)
	}
	fields := 6
	if completed {
		fields++
		if _, err = time.Parse(time.RFC3339Nano, delay["completed_at"].(string)); err != nil {
			t.Fatal(err)
		}
	}
	if failed {
		fields++
	}
	if len(delay) != fields {
		t.Fatalf("unexpected delay fields = %v", delay)
	}
}

// Script domain evidence, not its JSON: both real transports must map each
// lifecycle shape and satisfy their advertised schemas without executing waits.
func TestDelayHistoryPayloadLifecycleShapes(t *testing.T) {
	t.Parallel()
	const id = "aut_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	started := time.Date(2026, 10, 4, 12, 0, 0, 123456789, time.UTC)
	completed := started.Add(time.Second)
	failure := "core_stopping"
	document := delayDocument(`{"id":"wait","kind":"delay","duration_ms":86400000}`)
	definition, err := automations.DecodeDefinition(json.RawMessage(document))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		status    string
		runStatus string
		state     automations.RunState
		completed *time.Time
		failure   *string
	}{
		{status: "running", runStatus: "running", state: automations.RunningRun{}},
		{status: "completed", runStatus: "succeeded", state: automations.CompletedRun{
			CompletedAt: completed, Outcome: automations.SucceededRun{},
		}, completed: &completed},
		{status: "interrupted", runStatus: "interrupted", state: automations.CompletedRun{
			CompletedAt: completed, Outcome: automations.InterruptedRun{FailureCode: failure},
		}, completed: &completed, failure: &failure},
	}
	service := &delayHistoryAutomations{recordingAutomations: newRecordingAutomations()}
	for index, test := range cases {
		service.runs = append(service.runs, automations.Run{
			ID:           automations.RunID(fmt.Sprintf("arn_01890f47-7a6b-7c4d-8e9f-%012d", index+1)),
			AutomationID: id, AutomationName: "Wait", Revision: 2, Snapshot: definition,
			Cause: automations.ManualCause{}, ConditionDecision: automations.NotConfiguredDecision(),
			State: test.state, StartedAt: started,
			Delays: []automations.DelayExecution{
				{
					StepID:      "wait",
					Position:    0,
					DurationMS:  86400000,
					Status:      automations.DelayStatus(test.status),
					StartedAt:   started,
					DueAt:       started.Add(24 * time.Hour),
					CompletedAt: test.completed,
					FailureCode: test.failure,
				},
			},
		})
	}
	router, _ := newAutomationRouter(t, service)
	session := connectAutomationMCP(t, service)
	tools := delayTools(t, session)
	openapi := pageObject(t, restJSON(t, router, "/openapi.json"))
	schemas := []*jsonschema.Schema{
		compileDelayContractSchema(t, tools["get_automation_history_entry"].OutputSchema, ""),
		compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationHistoryEntryBody"),
	}
	t.Run("busy admission mapping", func(t *testing.T) {
		t.Parallel()
		response := performJSON(router, http.MethodPost, "/v1/automations/"+id+"/runs", "{}")
		if response.Code != http.StatusConflict || decodeAutomationProblem(t, response).Code != "automation_busy" {
			t.Fatalf("busy HTTP response = %d: %s", response.Code, response.Body.String())
		}
		result := callAutomationTool(t, session, "start_automation_run", map[string]any{"automation_id": id})
		if text := toolErrorText(t, result); !strings.Contains(text, "automation_busy") {
			t.Fatalf("busy MCP response = %s", text)
		}
	})
	for index, test := range cases {
		t.Run(test.status, func(t *testing.T) {
			t.Parallel()
			runID := string(service.runs[index].ID)
			entry := exactToolBody(t, callAutomationTool(t, session, "get_automation_history_entry", map[string]any{
				"automation_id": id, "entry_id": runID,
			}))
			assertDelayHTTPMatches(t, router, "/v1/automations/"+id+"/history/"+runID, entry)
			for _, schema := range schemas {
				assertDelaySchemaValid(t, schema, entry)
			}
			assertDelayHistoryPayload(t, entry, runID, document, test.runStatus, test.status)
		})
	}
}

func assertDelayHistoryPayload(t *testing.T, entry map[string]any, runID, document, runStatus, delayStatus string) {
	t.Helper()
	run := entry["run"].(map[string]any)
	if run["id"] != runID || run["status"] != runStatus || run["revision"] != json.Number("2") ||
		canonicalJSON(t, run["snapshot"]) != canonicalJSON(t, exactJSONObject(t, document)) {
		t.Fatalf("history identity/snapshot = %v", run)
	}
	delays := run["delays"].([]any)
	if len(delays) != 1 {
		t.Fatalf("delay evidence = %v", delays)
	}
	delay := delays[0].(map[string]any)
	assertDelayMetadata(t, delay, "wait", 0, 86400000, delayStatus)
	if delay["started_at"] != "2026-10-04T12:00:00.123456789Z" ||
		delay["due_at"] != "2026-10-05T12:00:00.123456789Z" {
		t.Fatalf("delay timestamps = %v", delay)
	}
	if delayStatus != "running" && delay["completed_at"] != "2026-10-04T12:00:01.123456789Z" {
		t.Fatalf("delay completion = %v", delay)
	}
	if delayStatus == "interrupted" && delay["failure_code"] != "core_stopping" {
		t.Fatalf("delay failure = %v", delay)
	}
}

type delayHistoryAutomations struct {
	*recordingAutomations

	runs []automations.Run
}

func (service *delayHistoryAutomations) StartManualRun(
	_ context.Context,
	_ automations.ManualRunInput,
) (automations.Run, error) {
	return automations.Run{}, automations.ErrAutomationBusy
}

func (service *delayHistoryAutomations) GetHistoryEntry(
	_ context.Context,
	id automations.AutomationID,
	entryID string,
) (automations.HistoryEntry, error) {
	for _, run := range service.runs {
		if run.AutomationID == id && string(run.ID) == entryID {
			return run, nil
		}
	}
	return nil, automations.ErrHistoryNotFound
}
