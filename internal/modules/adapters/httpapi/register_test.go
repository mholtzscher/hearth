package httpapi //nolint:testpackage // Public registration is checked without service invocation.

import "testing"

func TestRegisterIncludesManagementAndFunctions(t *testing.T) {
	t.Parallel()
	_, api := functionHTTP(t, nil)
	for path, id := range map[string]string{
		"/v1/adapters":                                       "list-adapters",
		"/v1/adapters/{adapter_id}":                          "get-adapter",
		"/v1/adapters/{adapter_id}/health/history":           "list-adapter-health-history",
		"/v1/adapters/{adapter_id}/functions":                "list-adapter-functions",
		"/v1/adapters/{adapter_id}/functions/{function_key}": "get-adapter-function",
		"/v1/adapters/{adapter_id}/function-invocations":     "list-adapter-function-invocations",
		"/v1/adapter-function-invocations/{invocation_id}":   "get-adapter-function-invocation",
	} {
		operation := api.OpenAPI().Paths[path].Get
		if operation == nil || operation.OperationID != id {
			t.Fatalf("%s=%+v", path, operation)
		}
	}
	operation := api.OpenAPI().Paths["/v1/adapters/{adapter_id}/functions/{function_key}/invocations"].Post
	if operation == nil || operation.OperationID != "invoke-adapter-function" {
		t.Fatalf("invoke=%+v", operation)
	}
	schema := operation.Responses["500"].Content["application/problem+json"].Schema
	if schema == nil || schema.Ref != "#/components/schemas/ErrorModel" {
		t.Fatalf("function problem contract=%+v", schema)
	}
}
