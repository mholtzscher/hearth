package mcpapi_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

func recursiveOutputSchema(t *testing.T) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal([]byte(`{
		"type":"object", "$ref":"#/$defs/node",
		"$defs":{"tag":{"$id":"tag.json","type":"string","enum":["leaf"]},"node":{
			"type":"object", "additionalProperties":false, "required":["value"],
			"properties":{
				"value":{"type":["integer","null"]},
				"tag":{"$ref":"tag.json"},
				"children":{"type":"array","items":{"$ref":"#/$defs/node"}}
			}
		}}
	}`), &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestOutputSchemaOverrideRejectsInvalidSchemaAtRegistration(t *testing.T) {
	t.Parallel()
	for _, exact := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "exact"}[exact], func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Fatal("registered an output schema with an unresolved local reference")
				}
			}()
			server := mcpapi.New(mcpapi.Config{Name: "invalid-schema", Version: "1"})
			mcpapi.Register(server, mcpapi.Tool[noInput, any]{
				Name: "invalid", ExactOutput: exact,
				OutputSchema: map[string]any{"type": "object", "$ref": "#/$defs/missing"},
				Handler:      func(context.Context, noInput) (any, error) { return struct{}{}, nil },
			})
		})
	}
}
