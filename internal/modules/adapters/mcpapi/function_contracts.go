//nolint:goconst,mnd // Literal canonical schema assertions keep the independent contracts readable.
package mcpapi

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

func functionToolSchema(properties map[string]*huma.Schema, required ...string) *huma.Schema {
	return &huma.Schema{
		Type: "object", AdditionalProperties: false, Properties: properties, Required: required,
	}
}

func textSchema() *huma.Schema   { return &huma.Schema{Type: "string"} }
func objectSchema() *huma.Schema { return &huma.Schema{Type: "object"} }

func collectionSchema(items *huma.Schema) *huma.Schema {
	return functionToolSchema(map[string]*huma.Schema{
		"items": {Type: "array", Items: items}, "next_cursor": textSchema(),
	}, "items")
}

func functionSummarySchema() *huma.Schema {
	registry := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	schema := huma.SchemaFromType(registry, reflect.TypeFor[FunctionSummary]())
	// Keep the existing JSON Schema integer, without an OpenAPI int64 annotation.
	schema.Properties["timeout_ms"].Format = ""
	return schema
}

func functionRecordSchema(detail bool) *huma.Schema {
	result := functionSummarySchema()
	if detail {
		// The documents are data, but still obey the canonical Function profile.
		result.Properties["input_schema"], result.Properties["output_schema"] = functionDocumentSchema(), functionDocumentSchema()
		result.Required = append(result.Required, "input_schema", "output_schema")
		result.Extensions = map[string]any{"$defs": map[string]*huma.Schema{"function_schema": functionSchemaNode()}}
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
func functionSchemaNode() *huma.Schema {
	ref := &huma.Schema{Ref: "#/$defs/function_schema"}
	stringList := &huma.Schema{
		Type: "array", Items: textSchema(), MaxItems: new(64), UniqueItems: true,
	}
	types := []any{"null", "boolean", "object", "array", "number", "integer", "string"}
	properties := map[string]*huma.Schema{
		"$schema":     {Const: "https://json-schema.org/draft/2020-12/schema"},
		"title":       {Type: "string", MaxLength: new(2000)},
		"description": {Type: "string", MaxLength: new(2000)},
		"type": {AnyOf: []*huma.Schema{
			{Enum: types},
			{Type: "array", MinItems: new(1), MaxItems: new(64), UniqueItems: true, Items: &huma.Schema{Enum: types}},
		}},
		"properties":           {Type: "object", AdditionalProperties: ref},
		"items":                ref,
		"additionalProperties": {AnyOf: []*huma.Schema{{Type: "boolean"}, ref}},
		"required":             stringList,
		"enum":                 {Type: "array", MinItems: new(1), MaxItems: new(64)},
		"const":                {}, // An arbitrary exact JSON value is intentional here.
		"uniqueItems":          {Type: "boolean"},
	}
	for _, name := range []string{"minItems", "maxItems", "minLength", "maxLength"} {
		properties[name] = &huma.Schema{Type: "integer", Minimum: new(float64(0))}
	}
	for _, name := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"} {
		properties[name] = &huma.Schema{Type: "number"}
	}
	properties["multipleOf"] = &huma.Schema{Type: "number", ExclusiveMinimum: new(float64(0))}
	return functionToolSchema(properties)
}
