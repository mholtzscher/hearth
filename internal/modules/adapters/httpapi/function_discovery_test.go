package httpapi //nolint:testpackage // Discovery contracts are checked through public registration.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func TestFunctionOpenAPIOperationIDsAndRunningAlternatives(t *testing.T) {
	t.Parallel()
	_, api := functionHTTP(t, &stubFunctions{})
	for path, operationID := range map[string]string{
		"/v1/adapters/{adapter_id}/functions":                "list-adapter-functions",
		"/v1/adapters/{adapter_id}/functions/{function_key}": "get-adapter-function",
		"/v1/adapters/{adapter_id}/function-invocations":     "list-adapter-function-invocations",
		"/v1/adapter-function-invocations/{invocation_id}":   "get-adapter-function-invocation",
	} {
		operation := api.OpenAPI().Paths[path].Get
		if operation == nil || operation.OperationID != operationID {
			t.Fatalf("%s operation=%+v", path, operation)
		}
	}
	operation := api.OpenAPI().Paths["/v1/adapter-function-invocations/{invocation_id}"].Get
	schema := compileFunctionSchema(t, operation.Responses["200"].Content["application/json"].Schema)
	running, err := json.Marshal(invocationBody(functionFixture(nil)))
	if err != nil {
		t.Fatal(err)
	}
	functionSchemaAccepts(t, schema, string(running), true)
	functionSchemaAccepts(
		t,
		schema,
		strings.Replace(string(running), `"completed_at":null`, `"completed_at":"2026-10-18T01:02:03Z"`, 1),
		false,
	)
	functionSchemaAccepts(t, schema, strings.TrimSuffix(string(running), "}")+`,"result":{}}`, false)
}

func TestFunctionOperationsRetainPublishedProblemResponses(t *testing.T) {
	t.Parallel()
	_, api := functionHTTP(t, &stubFunctions{})
	paths := api.OpenAPI().Paths
	operations := []*huma.Operation{
		paths["/v1/adapters/{adapter_id}/functions"].Get,
		paths["/v1/adapters/{adapter_id}/functions/{function_key}"].Get,
		paths["/v1/adapters/{adapter_id}/functions/{function_key}/invocations"].Post,
		paths["/v1/adapters/{adapter_id}/function-invocations"].Get,
		paths["/v1/adapter-function-invocations/{invocation_id}"].Get,
	}
	for _, operation := range operations {
		for _, status := range []int{
			http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusConflict,
			http.StatusServiceUnavailable, http.StatusInternalServerError,
		} {
			response := operation.Responses[strconv.Itoa(status)]
			if response == nil || response.Description != http.StatusText(status) {
				t.Fatalf("%s response %d = %#v", operation.OperationID, status, response)
			}
			media := response.Content["application/problem+json"]
			if media == nil || media.Schema == nil || media.Schema.Ref != "#/components/schemas/ErrorModel" {
				t.Errorf("%s response %d lost its published Problem Details schema", operation.OperationID, status)
			}
		}
	}
}
