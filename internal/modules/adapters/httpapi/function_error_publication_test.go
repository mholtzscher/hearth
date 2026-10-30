package httpapi //nolint:testpackage // Real routes and private fixtures verify the Function boundary.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestFunctionProblemsMatchPublishedSchemas(t *testing.T) {
	t.Parallel()
	type failure struct {
		cause  error
		code   string
		status int
	}
	for _, operation := range []struct {
		method, path, schemaPath string
		failures                 []failure
	}{
		{"GET", "/v1/adapters/demo/functions", "/v1/adapters/{adapter_id}/functions", []failure{
			{adapters.ErrAdapterNotFound, "adapter_not_found", 404},
			{adapters.ErrCatalogChanged, "catalog_changed", 409},
		}},
		{"GET", "/v1/adapters/demo/functions/inspect", "/v1/adapters/{adapter_id}/functions/{function_key}", []failure{
			{adapters.ErrFunctionNotFound, "function_not_found", 404},
		}},
		{"GET", "/v1/adapters/demo/function-invocations", "/v1/adapters/{adapter_id}/function-invocations", []failure{
			{adapters.ErrAdapterNotFound, "adapter_not_found", 404},
		}},
		{"GET", "/v1/adapter-function-invocations/" + functionInvocationID, "/v1/adapter-function-invocations/{invocation_id}", []failure{
			{adapters.ErrInvocationNotFound, "invocation_not_found", 404},
		}},
		{"POST", functionInvokePath, "/v1/adapters/{adapter_id}/functions/{function_key}/invocations", []failure{
			{adapters.ErrFunctionNotFound, "function_not_found", 404},
			{adapters.ErrCatalogChanged, "catalog_changed", 409},
			{adapters.ErrAdapterUnavailable, "adapter_unavailable", 503},
			{adapters.ErrFunctionAdmissionClosed, "admission_closed", 503},
			{&adapters.InvocationError{ID: functionInvocationID, Err: errors.Join(adapters.ErrFunctionPersistence, errors.New("SENSITIVE-persistence"))}, "internal_error", 500},
		}},
	} {
		// Every operation validates its input and can fail in persistence. Extra
		// operation-specific classes above follow its concrete service producers.
		operation.failures = append(
			operation.failures,
			failure{adapters.ErrInvalidFunctionInput, "invalid_input", 422},
			failure{
				errors.Join(adapters.ErrFunctionPersistence, errors.New("SENSITIVE-storage")),
				"internal_error",
				500,
			},
		)
		for index, test := range operation.failures {
			t.Run(fmt.Sprintf("%s %s/%d", operation.method, operation.schemaPath, index), func(t *testing.T) {
				t.Parallel()
				cause := fmt.Errorf("SENSITIVE-wrapper: %w", test.cause)
				stub := &stubFunctions{
					list: func(context.Context, string, adapters.PageInput) (adapters.FunctionPage, error) {
						return adapters.FunctionPage{}, cause
					},
					get: func(context.Context, string, adapters.FunctionKey) (adapters.Function, error) {
						return adapters.Function{}, cause
					},
					invoke: func(context.Context, adapters.InvokeInput) (adapters.Invocation, error) {
						return adapters.Invocation{}, cause
					},
					history: func(context.Context, adapters.InvocationFilter) (adapters.InvocationPage, error) {
						return adapters.InvocationPage{}, cause
					},
					detail: func(context.Context, adapters.InvocationID) (adapters.Invocation, error) {
						return adapters.Invocation{}, cause
					},
				}
				router, api := functionHTTP(t, stub)
				response := functionRequest(
					t,
					router,
					operation.method,
					operation.path,
					`{"catalog_revision":"`+functionRevision+`","arguments":{}}`,
				)
				if response.Code != test.status {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				var body map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body["code"] != test.code || body["status"] != float64(test.status) ||
					strings.Contains(response.Body.String(), "SENSITIVE") {
					t.Fatalf("incorrect or unsafe problem: %s", response.Body.String())
				}
				assertFunctionReceipt(t, response, body, test.cause)
				assertAdapterProblemSchema(t, api, operation.schemaPath, operation.method, response)
			})
		}
	}
}

func assertFunctionReceipt(t *testing.T, response *httptest.ResponseRecorder, body map[string]any, cause error) {
	t.Helper()
	var receipt any
	if _, ok := errors.AsType[*adapters.InvocationError](cause); ok {
		receipt = functionInvocationID
	}
	if body["invocation_id"] != receipt {
		t.Fatalf("incorrect Invocation receipt: %s", response.Body.String())
	}
	if body["$schema"] != nil || response.Header().Get("Link") != "" {
		t.Fatalf("publication changed wire schema links: %v %s", response.Header(), response.Body.String())
	}
}

func TestFunctionFrameworkProblemsMatchPublishedSchemas(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, method, path, schemaPath, body string
		status                               int
	}{
		{"list query", "GET", "/v1/adapters/demo/functions?limit=0", "/v1/adapters/{adapter_id}/functions", "", 422},
		{"history query", "GET", "/v1/adapters/demo/function-invocations?limit=0", "/v1/adapters/{adapter_id}/function-invocations", "", 422},
		{"missing body", "POST", functionInvokePath, "/v1/adapters/{adapter_id}/functions/{function_key}/invocations", "", 422},
		{"oversized", "POST", functionInvokePath, "/v1/adapters/{adapter_id}/functions/{function_key}/invocations", strings.Repeat(" ", maximumFunctionBodyBytes+2), 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router, api := functionHTTP(t, &stubFunctions{})
			response := functionRequest(t, router, test.method, test.path, test.body)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertAdapterProblemSchema(t, api, test.schemaPath, test.method, response)
		})
	}
}

func assertAdapterProblemSchema(t *testing.T, api huma.API, path, method string, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("wrong media type: %v", response.Header())
	}
	raw, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://hearth.invalid/functions"
	if err = compiler.AddResource(location, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(fmt.Sprintf("%s#/paths/%s/%s/responses/%d/content/application~1problem+json/schema",
		location, strings.ReplaceAll(path, "/", "~1"), strings.ToLower(method), response.Code))
	if err != nil {
		t.Fatal(err)
	}
	body, err := jsonschema.UnmarshalJSON(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(body); err != nil {
		t.Fatalf("problem violates publication: %v", err)
	}
	object, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("problem is not an object: %v", body)
	}
	object["undocumented"] = true
	if err = schema.Validate(object); err == nil {
		t.Fatal("problem schema permits undocumented fields")
	}
}

func TestFunctionOperationErrorCatalogs(t *testing.T) {
	t.Parallel()
	_, api := functionHTTP(t, &stubFunctions{})
	paths := api.OpenAPI().Paths
	for _, test := range []struct {
		operation *huma.Operation
		statuses  []int
	}{
		{paths["/v1/adapters/{adapter_id}/functions"].Get, []int{404, 409, 422, 500}},
		{paths["/v1/adapters/{adapter_id}/functions/{function_key}"].Get, []int{404, 422, 500}},
		{paths["/v1/adapters/{adapter_id}/function-invocations"].Get, []int{404, 422, 500}},
		{paths["/v1/adapter-function-invocations/{invocation_id}"].Get, []int{404, 422, 500}},
		{paths["/v1/adapters/{adapter_id}/functions/{function_key}/invocations"].Post, []int{404, 409, 413, 422, 500, 503}},
	} {
		var statuses []int
		for code := range test.operation.Responses {
			status, err := strconv.Atoi(code)
			if err != nil {
				t.Fatal(err)
			}
			if status >= http.StatusBadRequest {
				statuses = append(statuses, status)
			}
			if status >= http.StatusBadRequest &&
				test.operation.Responses[code].Description != http.StatusText(status) {
				t.Errorf("%s response %d lost its description", test.operation.OperationID, status)
			}
		}
		slices.Sort(statuses)
		if !slices.Equal(statuses, test.statuses) {
			t.Errorf("%s errors=%v, want=%v", test.operation.OperationID, statuses, test.statuses)
		}
	}
}
