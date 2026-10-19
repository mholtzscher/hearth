package api //nolint:testpackage // Shared fixtures exercise public HTTP and MCP registrations.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

const (
	functionRevision     = "afc_0190f47a-7a6b-7c4d-8e9f-0123456789ab"
	functionInvocationID = "afi_0190f47a-7a6b-7c4d-8e9f-0123456789ab"
	functionArguments    = `{"n":9007199254740993,"decimal":1.0000000000000001}`
	functionInvokePath   = "/v1/adapters/demo/functions/inspect/invocations"
)

type stubFunctions struct {
	list    func(context.Context, string, adapters.PageInput) (adapters.FunctionPage, error)
	get     func(context.Context, string, adapters.FunctionKey) (adapters.Function, error)
	invoke  func(context.Context, adapters.InvokeInput) (adapters.Invocation, error)
	history func(context.Context, adapters.InvocationFilter) (adapters.InvocationPage, error)
	detail  func(context.Context, adapters.InvocationID) (adapters.Invocation, error)
}

func (s *stubFunctions) ListFunctions(
	ctx context.Context,
	id string,
	page adapters.PageInput,
) (adapters.FunctionPage, error) {
	return s.list(ctx, id, page)
}

func (s *stubFunctions) GetFunction(
	ctx context.Context,
	id string,
	key adapters.FunctionKey,
) (adapters.Function, error) {
	return s.get(ctx, id, key)
}
func (s *stubFunctions) Invoke(ctx context.Context, input adapters.InvokeInput) (adapters.Invocation, error) {
	return s.invoke(ctx, input)
}

func (s *stubFunctions) ListInvocations(
	ctx context.Context,
	filter adapters.InvocationFilter,
) (adapters.InvocationPage, error) {
	return s.history(ctx, filter)
}
func (s *stubFunctions) GetInvocation(ctx context.Context, id adapters.InvocationID) (adapters.Invocation, error) {
	return s.detail(ctx, id)
}

func functionFixture(evidence adapters.Evidence) adapters.Invocation {
	started := time.Date(2026, 10, 18, 1, 2, 3, 456, time.UTC)
	invocation := adapters.Invocation{
		ID:              functionInvocationID,
		CatalogRevision: functionRevision,
		Function: adapters.Descriptor{
			Key:          "inspect",
			Description:  "Inspect once",
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
			TimeoutMS:    1000,
		},
		Route: adapters.RouteSnapshot{
			AdapterID: "demo",
			RuntimeID: "run_private",
		},
		Arguments: json.RawMessage(functionArguments),
		StartedAt: started,
		Deadline:  started.Add(time.Second),
	}
	if evidence != nil {
		invocation.Completion = &adapters.Completion{At: started.Add(time.Millisecond), Evidence: evidence}
	}
	return invocation
}

func functionHTTP(t *testing.T, service Functions) (http.Handler, huma.API) {
	t.Helper()
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Functions", "1"))
	RegisterFunctions(huma.NewGroup(api, "/v1"), service)
	return router, api
}

func functionRequest(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func functionMCP(t *testing.T, service Functions) (*mcp.ClientSession, *mcpapi.Server) {
	t.Helper()
	server := mcpapi.New(mcpapi.Config{Name: "functions-test", Version: "1"})
	RegisterFunctionsMCP(server, service)
	httpServer := httptest.NewServer(server.HTTPHandler())
	t.Cleanup(httpServer.Close)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "functions-client", Version: "1"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := session.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return session, server
}

func compileFunctionSchema(t *testing.T, schema any) *jsonschema.Schema {
	t.Helper()
	encoded, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://hearth.test/functions.json"
	if addErr := compiler.AddResource(location, document); addErr != nil {
		t.Fatal(addErr)
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func functionSchemaAccepts(t *testing.T, schema *jsonschema.Schema, value string, valid bool) {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(value))
	if err != nil {
		t.Fatal(err)
	}
	if validationErr := schema.Validate(document); (validationErr == nil) != valid {
		t.Fatalf("schema valid=%v, want %v: %v; value=%s", validationErr == nil, valid, validationErr, value)
	}
}

// Read the MCP wire before the official client's untyped StructuredContent
// decode loses number precision. Other tests exercise the official client.
func functionMCPWire(t *testing.T, server *mcpapi.Server, arguments string) json.RawMessage {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"invoke_adapter_function","arguments":`+arguments+`}}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	response := httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("MCP status %d: %s", response.Code, response.Body.String())
	}
	payload := response.Body.String()
	for line := range strings.SplitSeq(payload, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			payload = data
			break
		}
	}
	var envelope struct {
		Result struct {
			Structured json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		t.Fatalf("%s: %v", payload, err)
	}
	return envelope.Result.Structured
}
