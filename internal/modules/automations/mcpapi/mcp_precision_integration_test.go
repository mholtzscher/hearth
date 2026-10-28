//go:build integration

package mcpapi_test

import (
	"encoding/json"
	"testing"
)

// TestAutomationMCPDefinitionNumberSurvivesPersistenceAndToolRead checks exact
// input, repository storage, and client-visible definition output together.
func TestAutomationMCPDefinitionNumberSurvivesPersistenceAndToolRead(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	const literal = "9007199254740993"

	created := decodeStructuredInto[AutomationReceiptRead](t, callAutomationTool(
		t, session, "create_automation",
		json.RawMessage(`{"definition":`+precisionDefinitionDocument(t, literal)+`}`),
	))
	result := callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": created.ID})
	if result.IsError {
		t.Fatalf("get_automation failed: %s", toolErrorText(t, result))
	}
	body := exactToolBody(t, result)
	definition := body["definition"].(map[string]any)
	step := definition["steps"].([]any)[0].(map[string]any)
	if number := step["parameters"].(map[string]any)["level"]; number != json.Number(
		literal,
	) {
		t.Fatalf("persisted tool output number = %v, want exact %s", number, literal)
	}
}
