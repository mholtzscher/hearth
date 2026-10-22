//nolint:goconst // Literal JSON schema keywords and field names describe the outer contract.
package mcpapi

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

func functionPageProperties() map[string]any {
	return map[string]any{"adapter_id": textSchema(), "cursor": textSchema(),
		"limit": map[string]any{
			"type": "integer", "minimum": minPageLimit, "maximum": maxPageLimit, "default": defaultPageLimit,
		}}
}

func functionPageLimit(limit int) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return limit
}

func textSchema() map[string]any   { return map[string]any{"type": "string"} }
func objectSchema() map[string]any { return map[string]any{"type": "object"} }
