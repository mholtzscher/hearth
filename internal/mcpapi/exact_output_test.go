package mcpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

type exactOutput struct {
	Value int64 `json:"value"`
}

func TestExactOutputOverridePreservesRecursiveNumbersAndNull(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "exact-override", Version: "1"})
	const want = `{"value":9007199254740993,"children":[{"value":null}]}`
	schema := recursiveOutputSchema(t)
	schema["properties"] = map[string]any{
		"value": map[string]any{"const": json.Number("9007199254740993")},
	}
	mcpapi.Register(server, mcpapi.Tool[noInput, any]{
		Name: "exact", ExactOutput: true, OutputSchema: schema,
		Handler: func(context.Context, noInput) (any, error) { return json.RawMessage(want), nil },
	})
	mcpapi.RegisterWithRequest(server, mcpapi.ToolWithRequest[noInput, any]{
		Name: "request", ExactOutput: true, OutputSchema: schema,
		Handler: func(context.Context, *mcp.CallToolRequest, noInput) (any, error) {
			return json.RawMessage(want), nil
		},
	})
	for _, name := range []string{"exact", "request"} {
		result := callExactWireTool(t, server.HTTPHandler(), name, `{}`)
		if result.IsError || string(result.Structured) != want || len(result.Content) != 1 ||
			result.Content[0].Text != want {
			t.Fatalf("%s lost exact output: %+v", name, result)
		}
	}
}

// The output schema still describes an integer, even if a custom encoder lies.
type invalidExactOutput exactOutput

func (invalidExactOutput) MarshalJSON() ([]byte, error) {
	return []byte(`{"value":"not an integer"}`), nil
}

// Inspect the actual JSON-RPC wire bytes before an SDK client decodes float64.
func TestExactOutputPreservesStructuredAndTextNumbersAndSchemaSafety(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "exact", Version: "1"})
	mcpapi.Register(server, mcpapi.Tool[greetInput, exactOutput]{
		Name: "exact", ExactOutput: true,
		Handler: func(_ context.Context, input greetInput) (exactOutput, error) {
			if input.Name == "error" {
				return exactOutput{}, &mcpapi.ToolError{Code: "refused", Message: "refused"}
			}
			return exactOutput{Value: 9007199254740993}, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[noInput, invalidExactOutput]{
		Name: "invalid", ExactOutput: true,
		Handler: func(context.Context, noInput) (invalidExactOutput, error) { return invalidExactOutput{}, nil },
	})
	for _, test := range []struct {
		name, arguments string
		failure         bool
	}{
		{name: "exact", arguments: `{"name":"ok"}`},
		{name: "exact", arguments: `{}`, failure: true},
		{name: "exact", arguments: `{"name":"error"}`, failure: true},
		{name: "invalid", arguments: `{}`, failure: true},
	} {
		t.Run(test.name+test.arguments, func(t *testing.T) {
			t.Parallel()
			result := callExactWireTool(t, server.HTTPHandler(), test.name, test.arguments)
			if result.IsError != test.failure {
				t.Fatalf("unexpected tool result: %+v", result)
			}
			if !test.failure {
				const want = `{"value":9007199254740993}`
				if string(result.Structured) != want || len(result.Content) != 1 || result.Content[0].Text != want {
					t.Fatalf("rounded MCP structured/text wire result: %+v", result)
				}
			}
		})
	}
}

type exactWireResult struct {
	IsError    bool            `json:"isError"`
	Structured json.RawMessage `json:"structuredContent"`
	Content    []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func callExactWireTool(t *testing.T, handler http.Handler, name, arguments string) exactWireResult {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+arguments+`}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("MCP HTTP = %d: %s", response.Code, response.Body.String())
	}
	payload := response.Body.String()
	for line := range strings.SplitSeq(payload, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			payload = data
			break
		}
	}
	var envelope struct {
		Result exactWireResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		t.Fatalf("decode MCP wire %s: %v", payload, err)
	}
	return envelope.Result
}
