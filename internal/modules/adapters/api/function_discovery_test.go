package api //nolint:testpackage // Discovery parity is checked through public registrations.

import (
	"encoding/json"
	"strings"
	"testing"
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
