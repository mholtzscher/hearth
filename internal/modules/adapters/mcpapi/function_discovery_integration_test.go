//go:build integration

package mcpapi //nolint:testpackage // Exercises private service doubles through public tool registration.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestFunctionMCPRejectsOuterShapeBeforeService(t *testing.T) {
	t.Parallel()
	session, _ := functionMCP(t, &stubFunctions{})
	for _, arguments := range []string{
		`{}`, `{"adapter_id":null,"function_key":"inspect","catalog_revision":"x","arguments":{}}`,
		`{"adapter_id":"demo","function_key":null,"catalog_revision":"x","arguments":{}}`,
		`{"adapter_id":"demo","function_key":"inspect","catalog_revision":null,"arguments":{}}`,
		`{"adapter_id":"demo","function_key":"inspect","catalog_revision":"x","arguments":null}`,
		`{"adapter_id":"demo","function_key":"inspect","catalog_revision":"x","arguments":[]}`,
		`{"adapter_id":"demo","function_key":"inspect","catalog_revision":"x","arguments":{},"extra":true}`,
		`{"adapter_id":"demo","function_key":"inspect","catalog_revision":"x","arguments":{},"arguments":{}}`,
		`{"adapter_id":"demo","adapter_id":"other","function_key":"inspect","catalog_revision":"x","arguments":{}}`,
	} {
		result, err := session.CallTool(
			t.Context(),
			&mcp.CallToolParams{Name: "invoke_adapter_function", Arguments: json.RawMessage(arguments)},
		)
		if err != nil || !result.IsError {
			t.Fatalf("arguments=%s result=%+v err=%v", arguments, result, err)
		}
	}
	for _, limit := range []any{0, 201, nil, "50"} {
		result := mcpCallTool(
			t,
			session,
			"list_adapter_functions",
			map[string]any{"adapter_id": "demo", "limit": limit},
		)
		if !result.IsError {
			t.Fatalf("accepted limit=%v: %+v", limit, result)
		}
	}
}

func TestRegisterIncludesCompleteCatalogAndSelectedOutputSchemas(t *testing.T) {
	t.Parallel()
	session, _ := functionMCP(t, nil)
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"get_adapter",
		"get_adapter_function",
		"get_adapter_function_invocation",
		"invoke_adapter_function",
		"list_adapter_function_invocations",
		"list_adapter_functions",
		"list_adapter_health_history",
		"list_adapters",
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		if tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Fatalf("missing D3 contract: %+v", tool)
		}
		// D4 removes publication; until then keep every selected output contract
		// resolvable, including recursive Function descriptor documents.
		_ = compileFunctionSchema(t, tool.OutputSchema)
	}
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Fatalf("catalog=%v, want=%v", names, want)
	}
}

func TestManagementRejectsInvalidInputsBeforeService(t *testing.T) {
	t.Parallel()
	session := mcpSession(t, &stubAdapters{})
	for _, test := range []struct{ tool, arguments string }{
		{"get_adapter", `{"adapter_id":null}`}, {"get_adapter", `{"adapter_id":"Bad.Adapter"}`},
		{"get_adapter", `{"adapter_id":"demo","extra":true}`},
		{"list_adapters", `{"limit":0}`}, {"list_adapters", `{"limit":201}`}, {"list_adapters", `{"limit":null}`},
		{"list_adapters", `{"limit":"50"}`}, {"list_adapters", `{"cursor":"malformed"}`},
		{"list_adapter_health_history", `{"adapter_id":"demo","cursor":"malformed"}`},
	} {
		result, err := session.CallTool(
			t.Context(),
			&mcp.CallToolParams{Name: test.tool, Arguments: json.RawMessage(test.arguments)},
		)
		if err != nil || !result.IsError {
			t.Fatalf("%s %s: result=%+v err=%v", test.tool, test.arguments, result, err)
		}
	}
}

func TestFunctionInvokeMaximumBytesBeforeAdmission(t *testing.T) {
	t.Parallel()
	calls := 0
	stub := &stubFunctions{invoke: func(context.Context, adapters.InvokeInput) (adapters.Invocation, error) {
		calls++
		return functionFixture(adapters.Succeeded{Result: json.RawMessage(`{}`)}), nil
	}}
	session, _ := functionMCP(t, stub)
	prefix := `{"adapter_id":"demo","function_key":"inspect","catalog_revision":"` + functionRevision + `","arguments":{"padding":"`
	suffix := `"}}`
	for _, size := range []int{maximumFunctionBodyBytes, maximumFunctionBodyBytes + 1} {
		arguments := prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix
		result, err := session.CallTool(
			t.Context(),
			&mcp.CallToolParams{Name: "invoke_adapter_function", Arguments: json.RawMessage(arguments)},
		)
		if err != nil || result.IsError != (size > maximumFunctionBodyBytes) {
			t.Fatalf("size=%d result=%+v err=%v", size, result, err)
		}
	}
	if calls != 1 {
		t.Fatalf("admissions=%d, want exactly one", calls)
	}
}

func TestPublishedInvokeSchemaMatchesCompactSuccessAndFailure(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		evidence adapters.Evidence
	}{
		{"succeeded", adapters.Succeeded{Result: json.RawMessage(functionArguments)}},
		{"failed", adapters.Failed{ReasonCode: "upstream_refused"}},
		{"not_executed", adapters.NotExecuted{ReasonCode: "adapter_unavailable"}},
		{"unknown", adapters.Unknown{ReasonCode: "outcome_timeout"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubFunctions{invoke: func(context.Context, adapters.InvokeInput) (adapters.Invocation, error) {
				return functionFixture(test.evidence), nil
			}}
			session, server := functionMCP(t, stub)
			arguments := `{"adapter_id":"demo","function_key":"inspect","catalog_revision":"` + functionRevision + `","arguments":` + functionArguments + `}`
			result, err := session.CallTool(
				t.Context(),
				&mcp.CallToolParams{Name: "invoke_adapter_function", Arguments: json.RawMessage(arguments)},
			)
			if err != nil || result.IsError != (test.name != "succeeded") {
				t.Fatalf("result=%+v, err=%v", result, err)
			}
			tools, err := session.ListTools(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			wire := functionMCPWire(t, server, arguments)
			found := false
			for _, tool := range tools.Tools {
				if tool.Name != "invoke_adapter_function" {
					continue
				}
				found = true
				schema := compileFunctionSchema(t, tool.OutputSchema)
				functionSchemaAccepts(t, schema, string(wire), true)
				functionSchemaAccepts(
					t,
					schema,
					`{"id":"`+functionInvocationID+`","status":"succeeded","arguments":{},"result":{},"reason_code":"contradictory"}`,
					false,
				)
			}
			if !found {
				t.Fatal("invoke tool missing")
			}
		})
	}
}
