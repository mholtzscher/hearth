package agent //nolint:testpackage // Exercises the agent-only MCP extension boundary.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	automationmcp "github.com/mholtzscher/hearth/internal/modules/automations/mcpapi"
	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

//nolint:gocognit // Both tools exercise valid and invalid flags through the real MCP bridge.
func TestPresentationCompletionArgumentStaysAgentLocal(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"present_view", "present_automation"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := mcpapi.New(mcpapi.Config{Name: "completion-arguments", Version: "test"})
			var received string
			server.Raw().AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"number":     map[string]any{"type": "integer"},
					"definition": map[string]any{"type": "object"},
				},
				"required":             []string{"number"},
				"additionalProperties": false,
			}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				received = string(request.Params.Arguments)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
			})
			invoke := bridgeToolByName(t, mustBridgeTools(t, server), name)
			for _, value := range []string{"true", "false", `"true"`, "null"} {
				received = ""
				status := &toolResultStatus{}
				ctx := context.WithValue(t.Context(), toolResultStatusKey{}, status)
				out, err := invoke.InvokableRun(
					ctx,
					`{"number":9007199254740993,"definition":{"text":"<>&","value":1,"value":2},"complete_turn":`+value+`}`,
				)
				if err != nil {
					t.Fatal(err)
				}
				if value == "true" || value == "false" {
					if out != "ok" ||
						received != `{"definition":{"text":"<>&","value":1,"value":2},"number":9007199254740993}` ||
						status.modeledFailure ||
						status.completeTurn != (value == "true") {
						t.Fatalf("value=%s result=%q input=%q status=%+v", value, out, received, status)
					}
				} else if received != "" || !strings.Contains(out, "must be a boolean") || !status.modeledFailure || status.completeTurn {
					t.Fatalf("invalid value=%s result=%q input=%q status=%+v", value, out, received, status)
				}
			}
		})
	}
}

func TestAutomationCompletionSchemaPreservesClosedVariants(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "completion-schema", Version: "test"})
	automationmcp.Register(server, nil)
	info, err := bridgeToolByName(t, mustBridgeTools(t, server), "present_automation").Info(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := toolParameters(info)
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("urn:test:completion", parameters); err != nil {
		t.Fatal(err)
	}
	contract, err := compiler.Compile("urn:test:completion")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		arguments string
		valid     bool
	}{
		{`{"mode":"reference","automation_id":"auto","complete_turn":true}`, true},
		{`{"mode":"reference","automation_id":"auto","complete_turn":false}`, true},
		{`{"mode":"reference","automation_id":"auto"}`, true},
		{`{"mode":"reference","automation_id":"auto","complete_turn":"true"}`, false},
		{`{"mode":"reference","automation_id":"auto","unknown":true}`, false},
		{`{"mode":"reference","complete_turn":true}`, false},
	} {
		var input any
		if err = json.Unmarshal([]byte(test.arguments), &input); err != nil {
			t.Fatal(err)
		}
		if err = contract.Validate(input); (err == nil) != test.valid {
			t.Errorf("arguments=%s validation=%v want valid=%v", test.arguments, err, test.valid)
		}
	}
}

// An unlisted tool may have an effect, so it must not end a turn silently.
func TestOnlyReadOnlyToolsSkipTheOutcomeReply(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"present_view", "present_automation", "skill", "list_entities", "get_command", "query_entity_state_history"} {
		if requiresOutcomeReply(name) {
			t.Errorf("%s requires an outcome reply, want a silent finish allowed", name)
		}
	}
	for _, name := range []string{
		"execute_entity_command", "invoke_adapter_function", "send_notification", "start_automation_run",
		"delete_automation", "update_entity", "create_automation", "a_future_tool",
	} {
		if !requiresOutcomeReply(name) {
			t.Errorf("%s may finish silently, want an outcome reply", name)
		}
	}
}
