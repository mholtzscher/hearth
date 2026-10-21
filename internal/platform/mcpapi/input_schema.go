package mcpapi

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// InputContract owns the portable argument document. The SDK resolves it at
// registration and remains authoritative for argument validation and defaults.
type InputContract[I any] struct {
	document json.RawMessage
}

// PrepareInput derives or normalizes an argument schema without compiling a
// disposable validator. Caller-owned maps and bytes are never retained.
func PrepareInput[I any](schema any) (*InputContract[I], error) {
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
	return &InputContract[I]{document: raw}, nil
}

// defaultInputSchema derives the same argument schema the SDK derives from I, so
// advertising the document changes no constraint: a pointer argument describes
// its element type, and an untyped `any` argument is an empty object rather
// than an unconstrained value, because a tool's arguments are always an object.
func defaultInputSchema[I any]() (any, error) {
	if reflect.TypeFor[I]() == reflect.TypeFor[any]() {
		return &jsonschema.Schema{Type: schemaTypeObject}, nil
	}
	inputType := reflect.TypeFor[I]()
	if inputType.Kind() == reflect.Pointer {
		inputType = inputType.Elem()
	}
	schema, err := jsonschema.ForType(inputType, &jsonschema.ForOptions{})
	if err != nil {
		return nil, fmt.Errorf("derive input schema: %w", err)
	}
	return schema, nil
}

// InputSchemaWithProperty returns the argument schema for I with one declared
// object property replaced by a canonical JSON Schema document. The remaining
// properties keep the constraints their Go type and jsonschema tags describe,
// and the result is suitable for [PrepareInput].
//
// Use it when a property carries a strict, versioned document the Go type
// cannot express — such as a recursive, closed Automation definition — so the
// SDK validates that document before the handler runs and tools/list advertises
// the same schema. Reusing the canonical document keeps one schema authority
// rather than a hand-maintained MCP copy. property must name a property the
// derived schema declares.
func InputSchemaWithProperty[I any](property string, canonical json.RawMessage) (any, error) {
	schema, err := jsonschema.ForType(reflect.TypeFor[I](), &jsonschema.ForOptions{})
	if err != nil {
		return nil, fmt.Errorf("derive input schema: %w", err)
	}
	if _, declared := schema.Properties[property]; !declared {
		return nil, fmt.Errorf("derive input schema: property %q is not declared", property)
	}
	var document jsonschema.Schema
	if decodeErr := json.Unmarshal(canonical, &document); decodeErr != nil {
		return nil, fmt.Errorf("decode canonical input schema: %w", decodeErr)
	}
	schema.Properties[property] = &document
	return schema, nil
}
