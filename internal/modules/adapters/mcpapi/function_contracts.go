//nolint:goconst,mnd // Literal canonical schema assertions keep the independent contracts readable.
package mcpapi

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func collectionSchema(items *huma.Schema) *huma.Schema {
	return functionToolSchema(map[string]*huma.Schema{
		"items": {Type: "array", Items: items}, "next_cursor": textSchema(),
	}, "items")
}

func functionSummaryProperties() map[string]*huma.Schema {
	minimum, maximum := float64(adapters.MinimumFunctionTimeoutMS), float64(adapters.MaximumFunctionTimeoutMS)
	return map[string]*huma.Schema{
		"adapter_id":   {Type: "string", Pattern: `^[a-z0-9][a-z0-9_-]{0,62}$`},
		"function_key": {Type: "string", Pattern: `^[a-z0-9][a-z0-9_-]{0,62}$`},
		"catalog_revision": {
			Type:    "string",
			Pattern: `^afc_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
		},
		"description": {
			Type:      "string",
			MinLength: new(1),
			MaxLength: new(2000),
			Pattern:   `[^\x{0009}-\x{000d}\x{0020}\x{0085}\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}]`,
		},
		"callable":   {Type: "boolean"},
		"timeout_ms": {Type: "integer", Minimum: &minimum, Maximum: &maximum},
	}
}

func functionRecordSchema(detail bool) *huma.Schema {
	properties := functionSummaryProperties()
	required := []string{"adapter_id", "function_key", "catalog_revision", "description", "callable", "timeout_ms"}
	if detail {
		// The documents are data, but still obey the canonical Function profile.
		properties["input_schema"], properties["output_schema"] = functionDocumentSchema(), functionDocumentSchema()
		required = append(required, "input_schema", "output_schema")
	}
	result := functionToolSchema(properties, required...)
	if detail {
		result.Extensions = map[string]any{"$defs": map[string]any{"function_schema": functionSchemaNode()}}
	}
	return result
}

func functionDocumentSchema() *huma.Schema {
	return &huma.Schema{AllOf: []*huma.Schema{
		{Ref: "#/$defs/function_schema"},
		{Type: "object", Required: []string{"type"}, Properties: map[string]*huma.Schema{"type": {Const: "object"}}},
	}}
}

// Schema assertions use the supported canonical profile, not a permissive
// object placeholder. Compile also checks byte/depth/node bounds in the mapper.
func functionSchemaNode() map[string]any {
	ref := map[string]any{"$ref": "#/$defs/function_schema"}
	stringList := map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"maxItems":    64,
		"uniqueItems": true,
	}
	properties := map[string]any{
		"$schema":     map[string]any{"const": "https://json-schema.org/draft/2020-12/schema"},
		"title":       map[string]any{"type": "string", "maxLength": 2000},
		"description": map[string]any{"type": "string", "maxLength": 2000},
		"type": map[string]any{"anyOf": []any{
			map[string]any{"enum": []string{"null", "boolean", "object", "array", "number", "integer", "string"}},
			map[string]any{
				"type":        "array",
				"minItems":    1,
				"maxItems":    64,
				"uniqueItems": true,
				"items": map[string]any{
					"enum": []string{"null", "boolean", "object", "array", "number", "integer", "string"},
				},
			},
		}},
		"properties":           map[string]any{"type": "object", "additionalProperties": ref},
		"items":                ref,
		"additionalProperties": map[string]any{"anyOf": []any{map[string]any{"type": "boolean"}, ref}},
		"required":             stringList,
		"enum":                 map[string]any{"type": "array", "minItems": 1, "maxItems": 64},
		"const":                map[string]any{}, // An arbitrary exact JSON value is intentional here.
		"uniqueItems":          map[string]any{"type": "boolean"},
	}
	for _, name := range []string{"minItems", "maxItems", "minLength", "maxLength"} {
		properties[name] = map[string]any{"type": "integer", "minimum": 0}
	}
	for _, name := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"} {
		properties[name] = map[string]any{"type": "number"}
	}
	properties["multipleOf"] = map[string]any{"type": "number", "exclusiveMinimum": 0}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
}
