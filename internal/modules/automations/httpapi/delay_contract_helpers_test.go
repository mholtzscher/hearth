package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

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

func delayOperationContracts(t *testing.T, session *httpScenario) map[string]*httpOperationContract {
	t.Helper()
	listed, err := session.contracts()
	if err != nil {
		t.Fatal(err)
	}
	tools := make(map[string]*httpOperationContract, len(listed.Operations))
	for _, tool := range listed.Operations {
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

func delaySemanticInvalidDocuments() []string {
	predicate := `{"id":"matched","kind":"trigger","trigger_ids":["tick"]}`
	deep := `{"id":"wait","kind":"delay","duration_ms":{"literal":1}}`
	for index := range 8 {
		deep = fmt.Sprintf(`{"id":"if-%d","kind":"if","conditions":%s,"then":[%s]}`, index, predicate, deep)
	}
	var arms []string
	for arm := range 3 {
		var children []string
		for child := range 21 {
			children = append(
				children,
				fmt.Sprintf(`{"id":"wait-%d-%d","kind":"delay","duration_ms":{"literal":1}}`, arm, child),
			)
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

func assertDelayWriteRejected(t *testing.T, router http.Handler, session *httpScenario, id, bad string) {
	t.Helper()
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
			t.Fatalf("%s accepted invalid definition: %d %s", method, response.Code, response.Body.String())
		}
		if result := callHTTPScenario(t, session, tool, arguments); !result.Failed {
			t.Fatalf("%s accepted invalid definition", tool)
		}
	}
}

func assertCompletedDelayOnlyRun(t *testing.T, run map[string]any, stub *apiDevices) {
	t.Helper()
	if run["status"] != "succeeded" || stub.executionCount() != 0 {
		t.Fatalf("delay-only execution = %v, Commands = %d", run, stub.executionCount())
	}
	delays := contractExecutionsOfKind(t, run["executions"].([]any), "delay")
	if len(delays) != 2 {
		t.Fatalf("reached delays = %v", delays)
	}
	for index, stepID := range []string{"nested-wait", "last-wait"} {
		sequence := index + 2 // route and choose branch executions precede these Delays.
		assertDelayMetadata(t, delays[index], stepID, sequence, "succeeded")
	}
}

func contractExecutionsOfKind(t *testing.T, executions []any, kind string) []map[string]any {
	t.Helper()
	var found []map[string]any
	for _, item := range executions {
		execution := item.(map[string]any)
		if execution["kind"] == kind {
			found = append(found, execution)
		}
	}
	return found
}

func assertDelayHTTPMatches(t *testing.T, router http.Handler, path string, body map[string]any) {
	t.Helper()
	response := performJSON(router, http.MethodGet, path, "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, response.Code, response.Body.String())
	}
	if canonicalJSON(t, body) != canonicalJSON(t, exactJSONObject(t, response.Body.String())) {
		t.Fatalf("HTTP scenario/read %s differ: %s / %v", path, response.Body.String(), body)
	}
}

func writeDelayDefinition(
	t *testing.T,
	router http.Handler,
	session *httpScenario,
	transport, id, document string,
) map[string]any {
	t.Helper()
	method, path, status := http.MethodPost, "/v1/automations", http.StatusCreated
	body, tool := document, "create-automation"
	arguments := json.RawMessage(`{"definition":` + document + `}`)
	if id != "" {
		method, path, status = http.MethodPut, path+"/"+id, http.StatusOK
		body, tool = `{"expected_revision":1,"definition":`+document+`}`, "replace-automation"
		arguments = json.RawMessage(
			fmt.Sprintf(`{"automation_id":%q,"expected_revision":1,"definition":%s}`, id, document),
		)
	}
	if transport == "HTTP" {
		return exactHTTPScenarioBody(t, callHTTPScenario(t, session, tool, arguments))
	}
	response := performJSON(router, method, path, body)
	if response.Code != status {
		t.Fatalf("%s = %d: %s", method, response.Code, response.Body.String())
	}
	return exactJSONObject(t, response.Body.String())
}

func startDelayRun(t *testing.T, router http.Handler, session *httpScenario, transport, id string) map[string]any {
	t.Helper()
	if transport == "HTTP" {
		return exactHTTPScenarioBody(
			t,
			callHTTPScenario(t, session, "start-automation-run", map[string]any{"automation_id": id}),
		)
	}
	response := performJSON(router, http.MethodPost, "/v1/automations/"+id+"/runs", "{}")
	if response.Code != http.StatusAccepted {
		t.Fatalf("run = %d: %s", response.Code, response.Body.String())
	}
	return exactJSONObject(t, response.Body.String())
}

func assertDelayMetadata(t *testing.T, delay map[string]any, id string, sequence int, phase string) {
	t.Helper()
	if _, err := time.Parse(time.RFC3339Nano, delay["started_at"].(string)); err != nil {
		t.Fatal(err)
	}
	if delay["step_id"] != id || delay["sequence"] != json.Number(strconv.Itoa(sequence)) ||
		delay["kind"] != "delay" || delay["phase"] != phase {
		t.Fatalf("incorrect Delay execution: %v", delay)
	}
	evidence := delay["evidence"].(map[string]any)
	_, completed := delay["completed_at"]
	_, failed := evidence["reason_code"]
	if completed != (phase != "running") || failed != (phase == "interrupted") {
		t.Fatalf("terminal field presence = %v", delay)
	}
	fields := 6
	if completed {
		fields++
		if _, err := time.Parse(time.RFC3339Nano, delay["completed_at"].(string)); err != nil {
			t.Fatal(err)
		}
	}
	if len(delay) != fields || len(evidence) != boolInt(failed) {
		t.Fatalf("unexpected delay fields = %v", delay)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func waitForDelayEvidence(ctx context.Context, t *testing.T, service *automations.Service, id, runID string) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		entry, err := service.GetHistoryEntry(ctx, automations.AutomationID(id), runID)
		if err != nil {
			t.Fatal(err)
		}
		if run, ok := entry.(automations.Run); ok {
			for _, execution := range run.Executions {
				if _, isDelay := execution.Details.(automations.DelayExecutionDetails); isDelay {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("wait never started")
		case <-ticker.C:
		}
	}
}
