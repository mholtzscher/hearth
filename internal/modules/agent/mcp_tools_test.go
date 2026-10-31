package agent //nolint:testpackage // Tests exercise the in-memory MCP bridge internals.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
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

func TestMCPToolsBridgeExactFailureRetainsNumbers(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "exact-failure", Version: "test"})
	mcpapi.Register(server, mcpapi.Tool[struct{}, struct{}]{
		Name: "invoke_adapter_function",
		Handler: func(context.Context, struct{}) (struct{}, error) {
			return struct{}{}, &mcpapi.ToolError{
				Code:    "unknown",
				Message: "function invocation did not succeed",
				Details: map[string]any{
					"invocation": json.RawMessage(`{"arguments":{"n":9007199254740993},"status":"unknown"}`),
				},
			}
		},
	})
	invoke := bridgeToolByName(t, mustBridgeTools(t, server), "invoke_adapter_function")
	out, err := invoke.InvokableRun(t.Context(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `9007199254740993`) || strings.Contains(out, `9007199254740992`) {
		t.Fatalf("failure invocation arguments lost precision: %s", out)
	}
	var failure struct {
		Code string `json:"failure_code"`
	}
	if err = json.Unmarshal([]byte(out), &failure); err != nil || failure.Code != "unknown" {
		t.Fatalf("exact failure was not complete, nonduplicated JSON: %s, %v", out, err)
	}
}

func TestMCPToolsBridgeRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	bridged := mustBridgeTools(t, newBridgeTestServer())

	needsName := bridgeToolByName(t, bridged, "needs_count")
	for _, test := range []struct {
		name, arguments, feedback string
	}{
		{"missing required field", `{"count":2}`, "name"},
		{"malformed JSON", `{"name":`, "invalid arguments"},
		{"array root", `[]`, "JSON object"},
		{"string root", `"name"`, "JSON object"},
		{"number root", `2`, "JSON object"},
		{"boolean root", `true`, "JSON object"},
		{"null root", `null`, "JSON object"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			out, err := needsName.InvokableRun(t.Context(), test.arguments)
			if err != nil || !strings.Contains(out, test.feedback) {
				t.Fatalf("output = %q, error = %v, want recoverable feedback containing %q", out, err, test.feedback)
			}
		})
	}
}

type recoveryChatModel struct {
	fakeChatModel

	t     *testing.T
	steps int
}

func (script *recoveryChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return script, nil
}

func (script *recoveryChatModel) Generate(
	_ context.Context, messages []*schema.Message, _ ...model.Option,
) (*schema.Message, error) {
	script.steps++
	arguments := `{"count":2}`
	callID := "invalid-call"
	if script.steps > 1 {
		last := messages[len(messages)-1]
		want := "name"
		if script.steps == 3 {
			want = `"summary":"corrected"`
		}
		if last.Role != schema.Tool || last.ToolName != "needs_count" || !strings.Contains(last.Content, want) {
			script.t.Errorf("model observation = %+v, want needs_count tool feedback containing %q", last, want)
			return nil, errors.New("missing tool observation")
		}
		arguments = `{"name":"corrected","count":2}`
		callID = "corrected-call"
	}
	if script.steps == 3 {
		return schema.AssistantMessage("Recovered.", nil), nil
	}
	if script.steps > 3 {
		return nil, errors.New("unexpected extra model step")
	}
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID: callID, Type: "function",
		Function: schema.FunctionCall{Name: "needs_count", Arguments: arguments},
	}}), nil
}

func TestMCPToolsBridgeFailureClassification(t *testing.T) {
	t.Parallel()
	server := newBridgeTestServer()
	callCtx, cancelCall := context.WithCancel(t.Context())
	t.Cleanup(cancelCall)
	server.Raw().AddTool(&mcp.Tool{
		Name: "cancel_call", InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cancelCall()
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "rejected"}}}, nil
	})
	server.Raw().AddTool(&mcp.Tool{
		Name: "plain_failure", InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "try again"}}}, nil
	})
	protocolErr := &jsonrpc.Error{Code: -32603, Message: "protocol failure"}
	mcpapi.Register(server, mcpapi.Tool[bridgeEchoInput, bridgeEchoOutput]{
		Name: "protocol_failure",
		Handler: func(context.Context, bridgeEchoInput) (bridgeEchoOutput, error) {
			return bridgeEchoOutput{}, protocolErr
		},
	})
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Raw().Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "fatal-test"}, nil).
		Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if out, callErr := callMCPTool(t.Context(), session, "plain_failure", `{}`); callErr != nil || out != "try again" {
		t.Errorf("plain isError output = %q, error = %v, want recoverable observation", out, callErr)
	}
	if _, callErr := callMCPTool(callCtx, session, "cancel_call", `{}`); !errors.Is(callErr, context.Canceled) {
		t.Errorf("cancellation during invocation returned %v, want context.Canceled", callErr)
	}
	for _, test := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"canceled", func() context.Context { ctx, cancel := context.WithCancel(t.Context()); cancel(); return ctx }(), context.Canceled},
		{"deadline", func() context.Context {
			ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			t.Cleanup(cancel)
			return ctx
		}(), context.DeadlineExceeded},
	} {
		for _, arguments := range []string{`{"text":"hi"}`, `{"text":`, `null`} {
			if _, callErr := callMCPTool(test.ctx, session, "echo_text", arguments); !errors.Is(callErr, test.want) {
				t.Errorf("%s arguments %q: error = %v, want %v", test.name, arguments, callErr, test.want)
			}
		}
	}
	if _, callErr := callMCPTool(t.Context(), session, "protocol_failure", `{"text":"hi"}`); callErr != nil {
		if rpcErr, ok := errors.AsType[*jsonrpc.Error](callErr); !ok || rpcErr.Code != protocolErr.Code {
			t.Fatalf("protocol failure returned %v, want the JSON-RPC error preserved", callErr)
		}
	} else {
		t.Fatal("protocol failure became a recoverable observation")
	}
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if _, callErr := callMCPTool(t.Context(), session, "echo_text", `{"text":"hi"}`); callErr == nil {
		t.Fatal("closed session became a recoverable observation")
	}
}

func TestMCPToolsRequiresServer(t *testing.T) {
	t.Parallel()
	if _, err := MCPTools(t.Context(), nil); err == nil {
		t.Fatal("nil server produced no error")
	}
}
