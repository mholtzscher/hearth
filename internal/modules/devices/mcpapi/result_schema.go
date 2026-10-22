//nolint:goconst // Literal wire keys keep the fixed success contracts readable.
package mcpapi

import "github.com/danielgtaylor/huma/v2"

// Closed unions and their field-presence rules remain explicit.
func recordSchema(properties map[string]*huma.Schema, required ...string) *huma.Schema {
	return &huma.Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: false,
	}
}

func stringSchema() *huma.Schema { return &huma.Schema{Type: "string"} }

func pageSchema(item *huma.Schema) *huma.Schema {
	return recordSchema(map[string]*huma.Schema{
		"items": {Type: "array", Items: item}, "next_cursor": stringSchema(),
	}, "items")
}
