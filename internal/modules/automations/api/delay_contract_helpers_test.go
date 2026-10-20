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

func writeDelayDefinition(
	t *testing.T,
	router http.Handler,
	session *mcp.ClientSession,
	transport, id, document string,
) map[string]any {
	t.Helper()
	method, path, status := http.MethodPost, "/v1/automations", http.StatusCreated
	body, tool := document, "create_automation"
	arguments := json.RawMessage(`{"definition":` + document + `}`)
	if id != "" {
		method, path, status = http.MethodPut, path+"/"+id, http.StatusOK
		body, tool = `{"expected_revision":1,"definition":`+document+`}`, "replace_automation"
		arguments = json.RawMessage(
			fmt.Sprintf(`{"automation_id":%q,"expected_revision":1,"definition":%s}`, id, document),
		)
	}
	if transport == "MCP" {
		return exactToolBody(t, callAutomationTool(t, session, tool, arguments))
	}
	response := performJSON(router, method, path, body)
	if response.Code != status {
		t.Fatalf("%s = %d: %s", method, response.Code, response.Body.String())
	}
	return exactJSONObject(t, response.Body.String())
}

func startDelayRun(t *testing.T, router http.Handler, session *mcp.ClientSession, transport, id string) map[string]any {
	t.Helper()
	if transport == "MCP" {
		return exactToolBody(
			t,
			callAutomationTool(t, session, "start_automation_run", map[string]any{"automation_id": id}),
		)
	}
	response := performJSON(router, http.MethodPost, "/v1/automations/"+id+"/runs", "{}")
	if response.Code != http.StatusAccepted {
		t.Fatalf("run = %d: %s", response.Code, response.Body.String())
	}
	return exactJSONObject(t, response.Body.String())
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

func waitForDelayEvidence(ctx context.Context, t *testing.T, service *automations.Service, id, runID string) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		entry, err := service.GetHistoryEntry(ctx, automations.AutomationID(id), runID)
		if err != nil {
			t.Fatal(err)
		}
		if run, ok := entry.(automations.Run); ok && len(run.Delays) == 1 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("wait never started")
		case <-ticker.C:
		}
	}
}
