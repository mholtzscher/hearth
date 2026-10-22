package mcpapi_test

import (
	"encoding/json"
	"testing"
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
