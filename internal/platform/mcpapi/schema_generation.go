package mcpapi

import (
	"fmt"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// deriveToolSchema uses Huma only as a schema generator. The registry belongs
// to this derivation; it is neither an HTTP API nor a cross-server cache.
func deriveToolSchema(t reflect.Type) (*huma.Schema, error) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	registry := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	schema := huma.SchemaFromType(registry, t)
	if schema == nil {
		return nil, fmt.Errorf("cannot derive a JSON schema for %v", t)
	}
	prepareGeneratedSchema(schema)
	for _, definition := range registry.Map() {
		prepareGeneratedSchema(definition)
	}
	if len(registry.Map()) != 0 {
		if schema.Extensions == nil {
			schema.Extensions = make(map[string]any)
		}
		schema.Extensions["$defs"] = registry.Map()
	}
	return schema, nil
}

// Keep generated schemas in Hearth's existing document convention: no Go-width
// numeric formats and null-first nullable types.
// Imported canonical documents in Extensions are not reflected or rewritten.
func prepareGeneratedSchema(schema *huma.Schema) {
	if schema == nil {
		return
	}
	switch schema.Format {
	case "int32", "int64", "float", "double":
		schema.Format = ""
	}
	if schema.Nullable && schema.Type != "" {
		if schema.Extensions == nil {
			schema.Extensions = make(map[string]any)
		}
		schema.Extensions["type"] = []string{"null", schema.Type}
		schema.Nullable = false
	}
	for _, child := range schema.Properties {
		prepareGeneratedSchema(child)
	}
	for _, children := range [][]*huma.Schema{schema.OneOf, schema.AnyOf, schema.AllOf} {
		for _, child := range children {
			prepareGeneratedSchema(child)
		}
	}
	prepareGeneratedSchema(schema.Items)
	prepareGeneratedSchema(schema.Not)
	if additional, ok := schema.AdditionalProperties.(*huma.Schema); ok {
		prepareGeneratedSchema(additional)
	}
}
