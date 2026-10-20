package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func writeAutomationDefinition(
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

func startAutomationRun(
	t *testing.T,
	router http.Handler,
	session *mcp.ClientSession,
	transport, id string,
) map[string]any {
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
