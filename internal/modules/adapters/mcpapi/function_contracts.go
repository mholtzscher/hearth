//nolint:goconst,mnd // Literal canonical schema assertions keep the independent contracts readable.
package mcpapi

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

func functionSummarySchema() *huma.Schema {
	registry := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	schema := huma.SchemaFromType(registry, reflect.TypeFor[FunctionSummary]())
	// Keep the existing JSON Schema integer, without an OpenAPI int64 annotation.
	schema.Properties["timeout_ms"].Format = ""
	return schema
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
		Type: "array", Items: &huma.Schema{Type: "string"}, MaxItems: new(64), UniqueItems: true,
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
	return &huma.Schema{Type: "object", AdditionalProperties: false, Properties: properties}
}
