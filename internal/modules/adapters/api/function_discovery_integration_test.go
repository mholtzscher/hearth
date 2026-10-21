//go:build integration

package api //nolint:testpackage // Discovery parity is checked through public registrations.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestFunctionDiscoveryRoutesAndToolsPreserveCatalogData(t *testing.T) {
	t.Parallel()
	descriptor := functionFixture(nil).Function
	descriptor.InputSchema = json.RawMessage(`{"type":"object","properties":{"n":{"const":9007199254740993}}}`)
	function := adapters.Function{
		AdapterID:       "demo",
		CatalogRevision: functionRevision,
		Descriptor:      descriptor,
		Callable:        false,
	}
	stub := &stubFunctions{
		list: func(_ context.Context, adapterID string, page adapters.PageInput) (adapters.FunctionPage, error) {
			if adapterID != "demo" || page.Limit != 2 || page.Cursor != "opaque-scoped-cursor" {
				t.Fatalf("list=%s %+v", adapterID, page)
			}
			return adapters.FunctionPage{Items: []adapters.Function{function}, NextCursor: "next-scoped-cursor"}, nil
		},
		get: func(_ context.Context, adapterID string, key adapters.FunctionKey) (adapters.Function, error) {
			if adapterID != "demo" || key != "inspect" {
				t.Fatalf("get=%s %s", adapterID, key)
			}
			return function, nil
		},
	}
	router, api := functionHTTP(t, stub)
	for _, path := range []string{"/v1/adapters/demo/functions?limit=2&cursor=opaque-scoped-cursor", "/v1/adapters/demo/functions/inspect"} {
		response := functionRequest(t, router, http.MethodGet, path, "")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"const":9007199254740993`) ||
			!strings.Contains(response.Body.String(), `"callable":false`) {
			t.Fatalf("discovery=%d %s", response.Code, response.Body.String())
		}
	}
	session, _ := functionMCP(t, stub)
	for _, tool := range []struct {
		name string
		args map[string]any
	}{
		{"list_adapter_functions", map[string]any{"adapter_id": "demo", "limit": 2, "cursor": "opaque-scoped-cursor"}},
		{"get_adapter_function", map[string]any{"adapter_id": "demo", "function_key": "inspect"}},
	} {
		result := mcpCallTool(t, session, tool.name, tool.args)
		if result.IsError || len(result.Content) != 1 {
			t.Fatalf("tool=%+v", result)
		}
		text, ok := result.Content[0].(*mcp.TextContent)
		if !ok || !strings.Contains(text.Text, `"const":9007199254740993`) {
			t.Fatalf("rounded tool=%+v", result)
		}
	}
	requestSchema := api.OpenAPI().Paths["/v1/adapters/{adapter_id}/functions/{function_key}/invocations"].Post.RequestBody.Content["application/json"].Schema
	compiled := compileFunctionSchema(t, requestSchema)
	functionSchemaAccepts(t, compiled, `{"catalog_revision":"x","arguments":{}}`, true)
	for _, invalid := range []string{`{}`, `{"catalog_revision":null,"arguments":{}}`, `{"catalog_revision":"x","arguments":null}`, `{"catalog_revision":"x","arguments":{},"extra":true}`} {
		functionSchemaAccepts(t, compiled, invalid, false)
	}
}

func TestFunctionHistoryForwardsFilterAndCursorAndExposesRunningNull(t *testing.T) {
	t.Parallel()
	stub := &stubFunctions{
		history: func(_ context.Context, filter adapters.InvocationFilter) (adapters.InvocationPage, error) {
			if filter.AdapterID != "demo" || filter.FunctionKey != "removed" || filter.Limit != 200 ||
				filter.Cursor != "history-cursor" {
				t.Fatalf("filter=%+v", filter)
			}
			return adapters.InvocationPage{Items: []adapters.Invocation{functionFixture(nil)}}, nil
		},
	}
	router, _ := functionHTTP(t, stub)
	response := functionRequest(
		t,
		router,
		http.MethodGet,
		"/v1/adapters/demo/function-invocations?function_key=removed&limit=200&cursor=history-cursor",
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("history=%d %s", response.Code, response.Body.String())
	}
	want := response.Body.String()
	if !strings.Contains(want, `"completed_at":null`) || !strings.Contains(want, `"status":"running"`) ||
		strings.Contains(want, `"result"`) ||
		strings.Contains(want, `"reason_code"`) ||
		strings.Contains(want, `"next_cursor"`) {
		t.Fatalf("running shape=%s", want)
	}
	session, _ := functionMCP(t, stub)
	result := mcpCallTool(
		t,
		session,
		"list_adapter_function_invocations",
		map[string]any{"adapter_id": "demo", "function_key": "removed", "limit": 200, "cursor": "history-cursor"},
	)
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("tool=%+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || text.Text+"\n" != want {
		t.Fatalf("tool=%+v, HTTP=%s", result, want)
	}
}

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

func TestFunctionEmptyCollectionsUseArraysAndDefaultPageSize(t *testing.T) {
	t.Parallel()
	stub := &stubFunctions{
		list: func(_ context.Context, id string, page adapters.PageInput) (adapters.FunctionPage, error) {
			if id != "demo" || page.Limit != 50 {
				t.Fatalf("list=%s %+v", id, page)
			}
			return adapters.FunctionPage{}, nil
		},
	}
	router, _ := functionHTTP(t, stub)
	response := functionRequest(t, router, http.MethodGet, "/v1/adapters/demo/functions", "")
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"items":[]}` {
		t.Fatalf("empty collection=%d %s", response.Code, response.Body.String())
	}
	session, _ := functionMCP(t, stub)
	result := mcpCallTool(t, session, "list_adapter_functions", map[string]any{"adapter_id": "demo"})
	if result.IsError {
		t.Fatalf("default page=%+v", result)
	}
	for _, limit := range []string{"0", "201", "not-an-integer"} {
		rejected := functionRequest(t, router, http.MethodGet, "/v1/adapters/demo/functions?limit="+limit, "")
		if rejected.Code != http.StatusUnprocessableEntity {
			t.Fatalf("limit=%s status=%d body=%s", limit, rejected.Code, rejected.Body.String())
		}
	}
}
