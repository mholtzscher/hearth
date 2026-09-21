package mcpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

type greetInput struct {
	Name string `json:"name" jsonschema:"the name to greet"`
}

type greetOutput struct {
	Greeting string `json:"greeting"`
}

type noInput struct{}

// TestRegisterTypedToolRoundTrips proves a typed tool's decoded input reaches
// the handler and its typed output returns as structured content.
func TestRegisterTypedToolRoundTrips(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
		Name:        "greet",
		Description: "Greet one person",
		Handler: func(_ context.Context, input greetInput) (greetOutput, error) {
			return greetOutput{Greeting: "hello " + input.Name}, nil
		},
	})

	session := connectSession(t, server.HTTPHandler())
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "world"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", firstText(t, result))
	}
	var output greetOutput
	decodeStructured(t, result, &output)
	if output.Greeting != "hello world" {
		t.Fatalf("greeting = %q, want %q", output.Greeting, "hello world")
	}
}

// TestRegisterRejectsInvalidInputBeforeHandler proves the SDK validates
// arguments against the derived input schema and never runs the handler for
// invalid input.
//
// The rejection is an isError tool result, not a JSON-RPC protocol error. This
// pins the pinned-SDK limitation documented on the package: go-sdk v1.8.0
// mcp/server.go toolForErr returns `&errRes, nil` after `SetError`, so the
// wrapper never sees a protocol error to re-raise and deliberately keeps the
// SDK's shape. Changing this test means revisiting that decision and the
// accepted deviation from specs/mcp.md §5.
func TestRegisterRejectsInvalidInputBeforeHandler(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		arguments map[string]any
	}{
		{name: "missing required argument", arguments: map[string]any{}},
		{name: "wrong argument type", arguments: map[string]any{"name": 42}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var called atomic.Bool
			server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
			mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
				Name:        "greet",
				Description: "Greet one person",
				Handler: func(context.Context, greetInput) (greetOutput, error) {
					called.Store(true)
					return greetOutput{}, nil
				},
			})

			session := connectSession(t, server.HTTPHandler())
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
				Name:      "greet",
				Arguments: test.arguments,
			})
			if err != nil {
				t.Fatalf("call tool: %v", err)
			}
			if !result.IsError {
				t.Fatalf("result = %#v, want an isError tool result", result)
			}
			if !strings.Contains(firstText(t, result), "validating") {
				t.Fatalf("error text = %q, want the SDK's validation message", firstText(t, result))
			}
			if result.StructuredContent != nil {
				t.Fatalf("structured content = %#v, want none for an automatic rejection", result.StructuredContent)
			}
			if called.Load() {
				t.Fatal("handler ran for invalid input")
			}
		})
	}
}

// TestRegisterWithRequestPassesRawRequest proves the escape hatch reaches the
// raw MCP request while still returning a typed output.
func TestRegisterWithRequestPassesRawRequest(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	mcpapi.RegisterWithRequest(server, mcpapi.ToolWithRequest[noInput, greetOutput]{
		Name:        "request_tool_name",
		Description: "Report the invoked tool name",
		Handler: func(_ context.Context, request *mcp.CallToolRequest, _ noInput) (greetOutput, error) {
			return greetOutput{Greeting: request.Params.Name}, nil
		},
	})

	session := connectSession(t, server.HTTPHandler())
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "request_tool_name",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	var output greetOutput
	decodeStructured(t, result, &output)
	if output.Greeting != "request_tool_name" {
		t.Fatalf("greeting = %q, want the invoked tool name", output.Greeting)
	}
}

// TestToolErrorSurfacesAsIsErrorResult proves a domain ToolError crosses as an
// isError tool result whose text carries the stable code, not as an error the
// model never sees.
func TestToolErrorSurfacesAsIsErrorResult(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
		Name:        "greet",
		Description: "Greet one person",
		Handler: func(context.Context, greetInput) (greetOutput, error) {
			return greetOutput{}, &mcpapi.ToolError{Code: "entity_disabled", Message: "entity is disabled"}
		},
	})

	session := connectSession(t, server.HTTPHandler())
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "world"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !result.IsError {
		t.Fatalf("result = %#v, want isError", result)
	}
	text := firstText(t, result)
	if !strings.Contains(text, "entity_disabled") {
		t.Fatalf("error text = %q, want the stable failure code", text)
	}
}

// TestToolErrorPublishesStructuredFailureFields proves the machine-readable half
// of a domain failure reaches the client beside the human text: the stable
// failure code, the summary, and every detail the handler attached.
func TestToolErrorPublishesStructuredFailureFields(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
		Name:        "greet",
		Description: "Greet one person",
		Handler: func(context.Context, greetInput) (greetOutput, error) {
			return greetOutput{}, &mcpapi.ToolError{
				Code:    "entity_disabled",
				Message: "entity is disabled",
				Details: map[string]any{"status": "entity_disabled", "command_id": "cmd_1"},
			}
		},
	})

	session := connectSession(t, server.HTTPHandler())
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "world"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !result.IsError {
		t.Fatalf("result = %#v, want isError", result)
	}
	if got := result.StructuredContent; got == nil {
		t.Fatal("structured content = nil, want the failure fields")
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var fields map[string]any
	if unmarshalErr := json.Unmarshal(raw, &fields); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
	if fields["failure_code"] != "entity_disabled" {
		t.Fatalf("failure_code = %#v, want entity_disabled", fields["failure_code"])
	}
	if fields["message"] != "entity is disabled" {
		t.Fatalf("message = %#v, want the human summary", fields["message"])
	}
	if fields["status"] != "entity_disabled" {
		t.Fatalf("status = %#v, want the merged detail", fields["status"])
	}
	if fields["command_id"] != "cmd_1" {
		t.Fatalf("command_id = %#v, want the merged detail", fields["command_id"])
	}
}

// TestUnmodelledFailureIsGenericAndUnstructured proves a handler failure the
// wrapper does not model as a ToolError leaks no detail: the client sees only a
// generic message and no structured payload.
func TestUnmodelledFailureIsGenericAndUnstructured(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
		Name:        "greet",
		Description: "Greet one person",
		Handler: func(context.Context, greetInput) (greetOutput, error) {
			return greetOutput{}, errors.New("SQLite unavailable: /var/lib/hearth/hearth.db")
		},
	})

	session := connectSession(t, server.HTTPHandler())
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "world"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !result.IsError {
		t.Fatalf("result = %#v, want isError", result)
	}
	text := firstText(t, result)
	if strings.Contains(text, "SQLite") || strings.Contains(text, "hearth.db") {
		t.Fatalf("error text = %q, want no leaked detail", text)
	}
	if text != "internal error" {
		t.Fatalf("error text = %q, want the generic message", text)
	}
	if result.StructuredContent != nil {
		t.Fatalf("structured content = %#v, want nil for an unmodelled failure", result.StructuredContent)
	}
}

// TestProtocolErrorStaysAProtocolError proves the wrapper does not turn a
// JSON-RPC error into a tool result: the SDK must still raise it so the model
// never treats a protocol failure as a recoverable domain failure.
func TestProtocolErrorStaysAProtocolError(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
		Name:        "greet",
		Description: "Greet one person",
		Handler: func(context.Context, greetInput) (greetOutput, error) {
			return greetOutput{}, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "bad request"}
		},
	})

	session := connectSession(t, server.HTTPHandler())
	_, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "world"},
	})
	if err == nil {
		t.Fatal("CallTool succeeded, want a protocol error")
	}
	if _, ok := errors.AsType[*jsonrpc.Error](err); !ok {
		t.Fatalf("error = %T %v, want a jsonrpc.Error", err, err)
	}
}
