package mcpapi_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func assertMembers(t *testing.T, object map[string]any, want ...string) {
	t.Helper()
	var got []string
	for key := range object {
		got = append(got, key)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("members = %v, want %v: %v", got, want, object)
	}
}
func assertDefinitionWriteRejected(t *testing.T, session *mcp.ClientSession, id, bad string) {
	t.Helper()
	for _, tool := range []string{"create_automation", "replace_automation"} {
		arguments := json.RawMessage(`{"definition":` + bad + `}`)
		if tool == "replace_automation" {
			arguments = json.RawMessage(
				fmt.Sprintf(`{"automation_id":%q,"expected_revision":1,"definition":%s}`, id, bad),
			)
		}
		if result := callAutomationTool(t, session, tool, arguments); !result.IsError {
			t.Fatalf("%s accepted invalid document: %s", tool, bad)
		}
	}
}

func assertInputSchemasReject(t *testing.T, schemas []any, bad string) {
	t.Helper()
	for _, schema := range schemas {
		if compileDelayContractSchema(t, schema, "").Validate(exactJSONObject(t, bad)) == nil {
			t.Fatalf("discovery accepted %s", bad)
		}
	}
}
