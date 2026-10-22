package mcpapi

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// prepareInputSchema owns a portable document for SDK validation and defaults.
// Caller-owned maps and bytes are never retained.
func prepareInputSchema[I any](schema any) (json.RawMessage, error) {
	if schema == nil {
		var err error
		schema, err = defaultInputSchema[I]()
		if err != nil {
			return nil, err
		}
	}
	document, err := normalizePortableSchema(schema)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode input schema: %w", err)
	}
	return raw, nil
}

// defaultInputSchema derives MCP-owned payloads with their Huma constraint tags.
// Arguments always have an object root, including an untyped any input.
func defaultInputSchema[I any]() (any, error) {
	if reflect.TypeFor[I]() == reflect.TypeFor[any]() {
		return &huma.Schema{Type: schemaTypeObject}, nil
	}
	schema, err := deriveToolSchema(reflect.TypeFor[I]())
	if err != nil {
		return nil, fmt.Errorf("derive input schema: %w", err)
	}
	return schema, nil
}

// InputSchemaWithProperty returns the argument schema for I with one declared
// object property replaced by a canonical JSON Schema document. The remaining
// properties keep the constraints their Go type and Huma tags describe,
// and the result is suitable for Tool.InputSchema.
//
// Use it when a property carries a strict, versioned document the Go type
// cannot express — such as a recursive, closed Automation definition — so the
// SDK validates that document before the handler runs and tools/list advertises
// the same schema. Reusing the canonical document keeps one schema authority
// rather than a hand-maintained MCP copy. property must name a property the
// derived schema declares.
func InputSchemaWithProperty[I any](property string, canonical json.RawMessage) (any, error) {
	schema, err := deriveToolSchema(reflect.TypeFor[I]())
	if err != nil {
		return nil, fmt.Errorf("derive input schema: %w", err)
	}
	if _, declared := schema.Properties[property]; !declared {
		return nil, fmt.Errorf("derive input schema: property %q is not declared", property)
	}
	if !json.Valid(canonical) {
		return nil, fmt.Errorf("decode canonical input schema: invalid JSON")
	}
	// Keep canonical numeric literals and recursive documents out of reflection.
	var document map[string]json.RawMessage
	raw, encodeErr := json.Marshal(schema)
	if encodeErr != nil {
		return nil, fmt.Errorf("encode input schema: %w", encodeErr)
	}
	if decodeErr := json.Unmarshal(raw, &document); decodeErr != nil {
		return nil, fmt.Errorf("decode input schema: %w", decodeErr)
	}
	var properties map[string]json.RawMessage
	if decodeErr := json.Unmarshal(document["properties"], &properties); decodeErr != nil {
		return nil, fmt.Errorf("decode input properties: %w", decodeErr)
	}
	properties[property] = canonical
	document["properties"], err = json.Marshal(properties)
	if err != nil {
		return nil, fmt.Errorf("encode input properties: %w", err)
	}
	return document, nil
}
