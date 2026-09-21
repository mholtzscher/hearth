package agent //nolint:testpackage // Tests exercise the in-memory MCP bridge internals.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

type bridgeEchoInput struct {
	Text string `json:"text"`
}

type bridgeEchoOutput struct {
	Echo string `json:"echo"`
}

type bridgeCountInput struct {
	Name  string `json:"name"`
	Count *int   `json:"count,omitempty"`
}

type bridgeCountOutput struct {
	Summary string `json:"summary"`
}

func newBridgeTestServer() *mcpapi.Server {
	server := mcpapi.New(mcpapi.Config{Name: "hearth-agent-test", Version: "0.0.0"})
	mcpapi.Register(server, mcpapi.Tool[bridgeEchoInput, bridgeEchoOutput]{
		Name: "echo_text", Description: "Echo the input text back",
		Handler: func(_ context.Context, input bridgeEchoInput) (bridgeEchoOutput, error) {
			return bridgeEchoOutput{Echo: input.Text}, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[bridgeEchoInput, bridgeEchoOutput]{
		Name: "fail_example", Description: "Always fail with a domain error",
		Handler: func(_ context.Context, _ bridgeEchoInput) (bridgeEchoOutput, error) {
			return bridgeEchoOutput{}, &mcpapi.ToolError{
				Code:    "example_disabled",
				Message: "turned off",
				Details: map[string]any{"command_id": "cmd-1", "status": "failed"},
			}
		},
	})
	mcpapi.Register(server, mcpapi.Tool[bridgeCountInput, bridgeCountOutput]{
		Name: "needs_count", Description: "Require a name with an optional count",
		Handler: func(_ context.Context, input bridgeCountInput) (bridgeCountOutput, error) {
			return bridgeCountOutput{Summary: input.Name}, nil
		},
	})
	return server
}

func mustBridgeTools(t *testing.T, server *mcpapi.Server) []tool.BaseTool {
	t.Helper()
	bridged, err := MCPTools(t.Context(), server)
	if err != nil {
		t.Fatal(err)
	}
	return bridged
}

func bridgeToolByName(t *testing.T, bridged []tool.BaseTool, name string) tool.InvokableTool {
	t.Helper()
	for _, candidate := range bridged {
		info, err := candidate.Info(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name != name {
			continue
		}
		if info.ParamsOneOf == nil {
			t.Fatalf("%s has no parameter schema", name)
		}
		invokable, ok := candidate.(tool.InvokableTool)
		if !ok {
			t.Fatalf("%s is not invokable", name)
		}
		return invokable
	}
	t.Fatalf("no bridged tool named %s", name)
	return nil
}

func TestMCPToolsBridgeCatalog(t *testing.T) {
	t.Parallel()
	bridged := mustBridgeTools(t, newBridgeTestServer())
	if len(bridged) != 3 {
		t.Fatalf("bridged %d tools, want 3", len(bridged))
	}
	names := map[string]bool{}
	for _, candidate := range bridged {
		info, err := candidate.Info(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		names[info.Name] = true
		if info.Desc == "" {
			t.Fatalf("%s has no description", info.Name)
		}
	}
	for _, want := range []string{"echo_text", "fail_example", "needs_count"} {
		if !names[want] {
			t.Fatalf("bridged catalog missing %s: %v", want, names)
		}
	}
}

func TestMCPToolsBridgeInvoke(t *testing.T) {
	t.Parallel()
	bridged := mustBridgeTools(t, newBridgeTestServer())

	echo := bridgeToolByName(t, bridged, "echo_text")
	out, err := echo.InvokableRun(t.Context(), `{"text":"hi"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("echo result = %q, want the input text", out)
	}
}

// TestMCPToolsBridgeDomainFailureStaysRecoverable proves a modeled MCP failure
// reaches the model instead of ending the turn: the tool returns no Go error,
// and the structured failure payload (the stable code and the handler's
// details) survives beside the human-readable text.
func TestMCPToolsBridgeDomainFailureStaysRecoverable(t *testing.T) {
	t.Parallel()
	bridged := mustBridgeTools(t, newBridgeTestServer())

	failing := bridgeToolByName(t, bridged, "fail_example")
	out, err := failing.InvokableRun(t.Context(), `{"text":"hi"}`)
	if err != nil {
		t.Fatalf("modeled failure returned error %v, want a recoverable tool output", err)
	}
	for _, want := range []string{
		"example_disabled",
		`"failure_code":"example_disabled"`,
		`"command_id":"cmd-1"`,
		`"status":"failed"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("result = %q, want the structured failure field %s preserved", out, want)
		}
	}
}

// TestTracedToolRecordsRawArgumentObject proves the persisted tool call keeps the
// model's argument object: Function.Arguments must unmarshal as a JSON object,
// not the escaped JSON string literal a second marshal would produce.
func TestTracedToolRecordsRawArgumentObject(t *testing.T) {
	t.Parallel()
	service := newConversationStore(t, MinimumConversationRetention)
	conversation := mustCreateConversation(t, service)
	echo := bridgeToolByName(t, mustBridgeTools(t, newBridgeTestServer()), "echo_text")

	assembler := newTurnAssembler(context.Background(), service, conversation.ID, nil)
	ctx := withAssembler(context.Background(), assembler)
	if _, err := echo.InvokableRun(ctx, `{"text":"hi"}`); err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := service.database.QueryRow(
		`SELECT message_json FROM agent_messages WHERE conversation_id = ? AND role = ?`,
		conversation.ID, string(schema.Assistant),
	).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var call schema.Message
	if err := json.Unmarshal([]byte(stored), &call); err != nil {
		t.Fatal(err)
	}
	if len(call.ToolCalls) != 1 {
		t.Fatalf("stored tool calls = %d, want one", len(call.ToolCalls))
	}
	arguments := call.ToolCalls[0].Function.Arguments
	var object map[string]any
	if err := json.Unmarshal([]byte(arguments), &object); err != nil {
		t.Fatalf("Function.Arguments = %q, want a JSON argument object: %v", arguments, err)
	}
	if object["text"] != "hi" {
		t.Fatalf("Function.Arguments = %q, want the model's argument object", arguments)
	}
}

func TestMCPToolsBridgeRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	bridged := mustBridgeTools(t, newBridgeTestServer())

	needsName := bridgeToolByName(t, bridged, "needs_count")
	if _, err := needsName.InvokableRun(t.Context(), `{"count":2}`); err == nil {
		t.Fatal("missing required argument produced no error")
	}
}

func TestMCPToolsRequiresServer(t *testing.T) {
	t.Parallel()
	if _, err := MCPTools(t.Context(), nil); err == nil {
		t.Fatal("nil server produced no error")
	}
}
