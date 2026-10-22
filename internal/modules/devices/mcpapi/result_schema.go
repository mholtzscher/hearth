//nolint:goconst // Literal wire keys keep the fixed success contracts readable.
package mcpapi

import (
	"encoding/json"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// Ordinary records derive their field shape once. RawMessage represents any
// JSON value; operations narrow object-only fields and non-null collections.
func deriveResultSchema[O any]() *jsonschema.Schema {
	schema, err := jsonschema.For[O](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[json.RawMessage](): {}},
	})
	if err != nil {
		panic(err)
	}
	return schema
}

// Closed unions and their field-presence rules remain explicit.
func recordSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func stringSchema() map[string]any { return map[string]any{"type": "string"} }

func pageSchema(item any) map[string]any {
	return recordSchema(map[string]any{
		"items": map[string]any{"type": "array", "items": item}, "next_cursor": stringSchema(),
	}, "items")
}
