package mcpapi_test

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func assertInvalidCronNoWrite(
	t *testing.T,
	session *mcp.ClientSession,
	definition map[string]any,
	id, baseline string,
) {
	t.Helper()
	for _, tool := range []string{"create_automation", "replace_automation"} {
		args := map[string]any{"definition": definition}
		if tool == "replace_automation" {
			args["automation_id"], args["expected_revision"] = id, 1
		}
		assertCronMCPValidationError(t, callAutomationTool(t, session, tool, args), definition)
	}
	read := exactToolBody(t, callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": id}))
	if canonicalJSON(t, read) != baseline {
		t.Fatalf("rejected replacement changed record: %v", read)
	}
	page := exactToolBody(t, callAutomationTool(t, session, "list_automations", map[string]any{}))
	items := page["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["automation_id"] != id {
		t.Fatalf("rejected create wrote record: %v", page)
	}
	page = exactToolBody(
		t,
		callAutomationTool(t, session, "list_automation_history", map[string]any{"automation_id": id}),
	)
	if len(page["items"].([]any)) != 0 {
		t.Fatalf("invalid input wrote history: %v", page)
	}
}
