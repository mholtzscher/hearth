package mcpapi //nolint:testpackage // Pin independently owned MCP argument documents.

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Contract fixtures are explicit JSON, independent of the schema-building type.
// Switching builders must not change published defaults, bounds, or required fields.
func TestFunctionInputDocuments(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		schema any
		want   string
	}{
		{
			"list_adapter_functions", listFunctionsInputSchema(),
			`{"type":"object","additionalProperties":false,"properties":{"adapter_id":{"type":"string"},"cursor":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":200,"default":50}},"required":["adapter_id"]}`,
		},
		{
			"get_adapter_function", getFunctionInputSchema(),
			`{"type":"object","additionalProperties":false,"properties":{"adapter_id":{"type":"string"},"function_key":{"type":"string"}},"required":["adapter_id","function_key"]}`,
		},
		{
			"invoke_adapter_function", invokeFunctionInputSchema(),
			`{"type":"object","additionalProperties":false,"properties":{"adapter_id":{"type":"string"},"function_key":{"type":"string"},"catalog_revision":{"type":"string"},"arguments":{"type":"object"}},"required":["adapter_id","function_key","catalog_revision","arguments"]}`,
		},
		{
			"list_adapter_function_invocations", listInvocationsInputSchema(),
			`{"type":"object","additionalProperties":false,"properties":{"adapter_id":{"type":"string"},"cursor":{"type":"string"},"function_key":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":200,"default":50}},"required":["adapter_id"]}`,
		},
		{
			"get_adapter_function_invocation", getInvocationInputSchema(),
			`{"type":"object","additionalProperties":false,"properties":{"invocation_id":{"type":"string"}},"required":["invocation_id"]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(test.schema)
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err = json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal([]byte(test.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("input document = %s, want %s", raw, test.want)
			}
		})
	}
}
