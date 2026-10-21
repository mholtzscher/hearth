//nolint:goconst // Literal JSON schema keywords and field names describe the outer contract.
package mcpapi

import "github.com/danielgtaylor/huma/v2"

type functionInput struct {
	AdapterID   string `json:"adapter_id"`
	FunctionKey string `json:"function_key"`
}
type functionPageInput struct {
	AdapterID string `json:"adapter_id"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

func functionToolSchema(properties map[string]*huma.Schema, required ...string) *huma.Schema {
	return &huma.Schema{Type: "object", AdditionalProperties: false, Properties: properties, Required: required}
}

func functionPageProperties() map[string]*huma.Schema {
	minimum, maximum := float64(minPageLimit), float64(maxPageLimit)
	return map[string]*huma.Schema{"adapter_id": textSchema(), "cursor": textSchema(),
		"limit": {Type: "integer", Minimum: &minimum, Maximum: &maximum, Default: defaultPageLimit}}
}

func functionPageLimit(limit int) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return limit
}

func textSchema() *huma.Schema   { return &huma.Schema{Type: "string"} }
func objectSchema() *huma.Schema { return &huma.Schema{Type: "object"} }
