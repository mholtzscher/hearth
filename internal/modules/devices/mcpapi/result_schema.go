//nolint:goconst // Literal wire keys keep the fixed success contracts readable.
package mcpapi

// These helpers compose the fixed result records. Opaque JSON fields are
// selected explicitly by the operation rather than reflected as byte arrays.
func recordSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func stringSchema() map[string]any { return map[string]any{"type": "string"} }

func nullableSchema(schema map[string]any) map[string]any {
	return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
}

func pageSchema(item map[string]any) map[string]any {
	return recordSchema(map[string]any{
		"items": map[string]any{"type": "array", "items": item}, "next_cursor": stringSchema(),
	}, "items")
}
