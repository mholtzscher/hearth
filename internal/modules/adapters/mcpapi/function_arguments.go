//nolint:goconst // Literal JSON schema keywords and field names describe the outer contract.
package mcpapi

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
)

type functionInput struct {
	AdapterID   string `json:"adapter_id"`
	FunctionKey string `json:"function_key"`
}
type functionPageInput struct {
	AdapterID string `json:"adapter_id"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

func functionToolSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type": "object", "additionalProperties": false, "properties": properties,
	}
	if len(required) != 0 {
		schema["required"] = required
	}
	return schema
}

func functionPageInputSchema[I any]() *jsonschema.Schema {
	schema, err := jsonschema.For[I](nil)
	if err != nil {
		panic(err)
	}
	limit := schema.Properties["limit"]
	limit.Minimum = new(float64(minPageLimit))
	limit.Maximum = new(float64(maxPageLimit))
	limit.Default = json.RawMessage("50")
	return schema
}

func functionPageLimit(limit int) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return limit
}

func textSchema() map[string]any   { return map[string]any{"type": "string"} }
func objectSchema() map[string]any { return map[string]any{"type": "object"} }
