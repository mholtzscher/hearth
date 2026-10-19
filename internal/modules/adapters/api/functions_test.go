package api //nolint:testpackage // Public registrations are exercised with shared transport fixtures.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestFunctionHTTPValidatesOuterBodyAndBoundsBeforeAdmission(t *testing.T) {
	t.Parallel()
	valid := `{"catalog_revision":"` + functionRevision + `","arguments":{}}`
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{"empty", "", 422}, {"missing revision", `{"arguments":{}}`, 422},
		{"missing arguments", `{"catalog_revision":"x"}`, 422},
		{"null revision", `{"catalog_revision":null,"arguments":{}}`, 422},
		{"null arguments", `{"catalog_revision":"x","arguments":null}`, 422},
		{"array arguments", `{"catalog_revision":"x","arguments":[]}`, 422},
		{"unknown", `{"catalog_revision":"x","arguments":{},"secret":"never-echo"}`, 422},
		{"duplicate", `{"catalog_revision":"x","arguments":{},"arguments":{}}`, 422},
		{"trailing", valid + `{}`, 422}, {"malformed", `{"secret":"never-echo"`, 422},
		{"oversized malformed", strings.Repeat("!", maximumFunctionBodyBytes+1), 413},
		{"exact bound", valid + strings.Repeat(" ", maximumFunctionBodyBytes-len(valid)), 200},
		{"over bound", valid + strings.Repeat(" ", maximumFunctionBodyBytes-len(valid)+1), 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			stub := &stubFunctions{
				invoke: func(_ context.Context, in adapters.InvokeInput) (adapters.Invocation, error) {
					calls++
					if in.AdapterID != "demo" || in.FunctionKey != "inspect" ||
						string(in.CatalogRevision) != functionRevision ||
						string(in.Arguments) != "{}" {
						t.Fatalf("input=%+v", in)
					}
					return functionFixture(adapters.Succeeded{Result: json.RawMessage(`{}`)}), nil
				},
			}
			router, _ := functionHTTP(t, stub)
			response := functionRequest(t, router, http.MethodPost, functionInvokePath, test.body)
			if response.Code != test.status || strings.Contains(response.Body.String(), "never-echo") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			wantCalls := 0
			if test.status == http.StatusOK {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("admissions=%d, want %d", calls, wantCalls)
			}
		})
	}
}

//nolint:gocognit // One evidence fixture is compared across three public transports.
func TestFunctionTerminalEvidenceHTTPMCPAndResources(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		status   string
		evidence adapters.Evidence
	}{
		{"succeeded", adapters.Succeeded{Result: json.RawMessage(functionArguments)}},
		{"failed", adapters.Failed{ReasonCode: "upstream_refused"}},
		{"not_executed", adapters.NotExecuted{ReasonCode: "adapter_unavailable"}},
		{"unknown", adapters.Unknown{ReasonCode: "outcome_timeout"}},
	} {
		t.Run(test.status, func(t *testing.T) {
			t.Parallel()
			invocation := functionFixture(test.evidence)
			stub := &stubFunctions{
				invoke: func(_ context.Context, in adapters.InvokeInput) (adapters.Invocation, error) {
					if string(in.Arguments) != functionArguments {
						t.Fatalf("rounded arguments: %s", in.Arguments)
					}
					return invocation, nil
				},
				detail: func(_ context.Context, id adapters.InvocationID) (adapters.Invocation, error) {
					if id != functionInvocationID {
						t.Fatalf("id=%s", id)
					}
					return invocation, nil
				},
				history: func(_ context.Context, filter adapters.InvocationFilter) (adapters.InvocationPage, error) {
					if filter.AdapterID != "demo" || filter.Limit != 50 {
						t.Fatalf("filter=%+v", filter)
					}
					return adapters.InvocationPage{Items: []adapters.Invocation{invocation}}, nil
				},
			}
			router, api := functionHTTP(t, stub)
			body := `{"catalog_revision":"` + functionRevision + `","arguments":` + functionArguments + `}`
			response := functionRequest(t, router, http.MethodPost, functionInvokePath, body)
			if response.Code != http.StatusOK ||
				response.Header().Get("Location") != "/v1/adapter-function-invocations/"+functionInvocationID {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
			assertFunctionEvidence(t, response.Body.Bytes(), test.status)
			for _, path := range []string{"/v1/adapter-function-invocations/" + functionInvocationID, "/v1/adapters/demo/function-invocations"} {
				read := functionRequest(t, router, http.MethodGet, path, "")
				if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"status":"`+test.status+`"`) ||
					!strings.Contains(read.Body.String(), "9007199254740993") {
					t.Fatalf("read=%d %s", read.Code, read.Body.String())
				}
			}
			operation := api.OpenAPI().Paths["/v1/adapters/{adapter_id}/functions/{function_key}/invocations"].Post
			if operation == nil || operation.OperationID != "invoke-adapter-function" {
				t.Fatalf("operation=%+v", operation)
			}
			openapiSchema := compileFunctionSchema(t, operation.Responses["200"].Content["application/json"].Schema)
			functionSchemaAccepts(t, openapiSchema, response.Body.String(), true)
			contradictory := strings.TrimSuffix(response.Body.String(), "\n")
			contradictory = strings.TrimSuffix(contradictory, "}") + `,"result":{},"reason_code":"contradictory"}`
			functionSchemaAccepts(t, openapiSchema, contradictory, false)
			session, server := functionMCP(t, stub)
			arguments := `{"adapter_id":"demo","function_key":"inspect","catalog_revision":"` + functionRevision + `","arguments":` + functionArguments + `}`
			result, err := session.CallTool(
				t.Context(),
				&mcp.CallToolParams{Name: "invoke_adapter_function", Arguments: json.RawMessage(arguments)},
			)
			if err != nil || result.IsError != (test.status != "succeeded") {
				t.Fatalf("tool=%+v error=%v", result, err)
			}
			wire := functionMCPWire(t, server, arguments)
			retained := wire
			if test.status != "succeeded" {
				var failure struct {
					Code       string          `json:"failure_code"`
					Invocation json.RawMessage `json:"invocation"`
				}
				if decodeErr := json.Unmarshal(wire, &failure); decodeErr != nil {
					t.Fatal(decodeErr)
				}
				if failure.Code != test.status {
					t.Fatalf("failure=%s", wire)
				}
				retained = failure.Invocation
			}
			assertFunctionEvidence(t, retained, test.status)
			tools, err := session.ListTools(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, tool := range tools.Tools {
				if tool.Name == "invoke_adapter_function" {
					schema := compileFunctionSchema(t, tool.OutputSchema)
					functionSchemaAccepts(t, schema, string(wire), true)
					functionSchemaAccepts(t, schema, contradictory, false)
				}
			}
			readTool := mcpCallTool(
				t,
				session,
				"get_adapter_function_invocation",
				map[string]any{"invocation_id": functionInvocationID},
			)
			if readTool.IsError || len(readTool.Content) != 1 {
				t.Fatalf("read tool=%+v", readTool)
			}
			readText, ok := readTool.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("read content=%+v", readTool.Content)
			}
			assertFunctionEvidence(t, []byte(readText.Text), test.status)
			for _, uri := range []string{"hearth://adapter-function-invocation/" + functionInvocationID, "hearth://adapter/demo/function-invocations"} {
				resource, readErr := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri})
				if readErr != nil || len(resource.Contents) != 1 ||
					!strings.Contains(resource.Contents[0].Text, "9007199254740993") ||
					!strings.Contains(resource.Contents[0].Text, `"status":"`+test.status+`"`) {
					t.Fatalf("resource=%+v err=%v", resource, readErr)
				}
			}
		})
	}
}

func assertFunctionEvidence(t *testing.T, data []byte, status string) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["status"]) != `"`+status+`"` || string(fields["arguments"]) != functionArguments ||
		string(fields["completed_at"]) == "null" {
		t.Fatalf("evidence=%s", data)
	}
	_, resultPresent := fields["result"]
	_, reasonPresent := fields["reason_code"]
	if resultPresent != (status == "succeeded") || reasonPresent != (status != "succeeded") {
		t.Fatalf("contradictory evidence=%s", data)
	}
	if resultPresent && string(fields["result"]) != functionArguments {
		t.Fatalf("rounded result=%s", data)
	}
	for _, private := range []string{"runtime_id", "descriptor", "correlation_id"} {
		if _, present := fields[private]; present {
			t.Fatalf("private %s exposed: %s", private, data)
		}
	}
}

func TestFunctionErrorsAreSafeAndRetainKnownInvocationID(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{adapters.ErrInvalidFunctionInput, 422, "invalid_input"}, {adapters.ErrAdapterNotFound, 404, "adapter_not_found"},
		{adapters.ErrFunctionNotFound, 404, "function_not_found"}, {adapters.ErrInvocationNotFound, 404, "invocation_not_found"},
		{adapters.ErrCatalogChanged, 409, "catalog_changed"}, {adapters.ErrCatalogConflict, 409, "catalog_conflict"},
		{adapters.ErrAdapterUnavailable, 503, "adapter_unavailable"}, {adapters.ErrFunctionAdmissionClosed, 503, "admission_closed"},
		{adapters.ErrFunctionPersistence, 500, "internal_error"}, {errors.New("secret-database-path"), 500, "internal_error"},
		{&adapters.InvocationError{ID: functionInvocationID, Err: adapters.ErrFunctionPersistence}, 500, "internal_error"},
		{&adapters.InvocationError{ID: functionInvocationID, Err: errors.Join(adapters.ErrFunctionPersistence, adapters.ErrInvalidFunctionInput)}, 500, "internal_error"},
	} {
		t.Run(test.err.Error(), func(t *testing.T) {
			t.Parallel()
			stub := &stubFunctions{invoke: func(context.Context, adapters.InvokeInput) (adapters.Invocation, error) {
				return adapters.Invocation{}, fmt.Errorf("secret-validator-value: %w", test.err)
			}}
			router, _ := functionHTTP(t, stub)
			response := functionRequest(
				t,
				router,
				http.MethodPost,
				functionInvokePath,
				`{"catalog_revision":"`+functionRevision+`","arguments":{}}`,
			)
			if response.Code != test.status || strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
			var known *adapters.InvocationError
			wantID := errors.As(test.err, &known)
			if strings.Contains(response.Body.String(), functionInvocationID) != wantID {
				t.Fatalf("known ID lost: %s", response.Body.String())
			}
			session, _ := functionMCP(t, stub)
			result := mcpCallTool(
				t,
				session,
				"invoke_adapter_function",
				map[string]any{
					"adapter_id":       "demo",
					"function_key":     "inspect",
					"catalog_revision": functionRevision,
					"arguments":        map[string]any{},
				},
			)
			encoded, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || !strings.Contains(string(encoded), `"failure_code":"`+test.code+`"`) ||
				strings.Contains(string(encoded), "secret") ||
				strings.Contains(string(encoded), functionInvocationID) != wantID {
				t.Fatalf("tool=%s", encoded)
			}
		})
	}
}
