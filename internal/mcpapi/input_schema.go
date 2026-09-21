package mcpapi

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// portableInputSchema returns the argument JSON Schema a typed Tool advertises,
// rewritten by [portableSchema]. A nil override asks for the schema the SDK
// would derive from I, which the wrapper derives itself through
// [defaultInputSchema]; a non-nil override is normalized as given. The SDK still
// resolves and enforces whichever schema is advertised.
func portableInputSchema[I any](override any) any {
	if override != nil {
		return portableSchema(override)
	}
	return portableSchema(defaultInputSchema[I]())
}

// defaultInputSchema derives the same argument schema the SDK derives from I, so
// advertising the document changes no constraint: a pointer argument describes
// its element type, and an untyped `any` argument is an empty object rather
// than an unconstrained value, because a tool's arguments are always an object.
func defaultInputSchema[I any]() any {
	if reflect.TypeFor[I]() == reflect.TypeFor[any]() {
		return &jsonschema.Schema{Type: schemaTypeObject}
	}
	inputType := reflect.TypeFor[I]()
	if inputType.Kind() == reflect.Pointer {
		inputType = inputType.Elem()
	}
	schema, err := jsonschema.ForType(inputType, &jsonschema.ForOptions{})
	if err != nil {
		// The SDK derives the same schema in AddTool, which reports the same
		// failure; returning nil keeps that one error message authoritative.
		return nil
	}
	return schema
}

// InputSchemaWithProperty returns the argument schema for I with one declared
// object property replaced by a canonical JSON Schema document. The remaining
// properties keep the constraints their Go type and jsonschema tags describe,
// and the result is suitable for [Tool.InputSchema] and
// [ToolWithRequest.InputSchema].
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
