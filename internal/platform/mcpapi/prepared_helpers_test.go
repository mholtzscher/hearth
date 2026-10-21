package mcpapi_test

import (
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func preparedInput[I any](t *testing.T, schema any) *mcpapi.InputContract[I] {
	t.Helper()
	contract, err := mcpapi.PrepareInput[I](schema)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func preparedOutput[O any](t *testing.T, schema any) *mcpapi.OutputContract[O] {
	t.Helper()
	contract, err := mcpapi.PrepareOutput[O](schema)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

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
