package mcpapi

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// InputSchemaWithProperty returns the argument schema for I with one declared
// object property replaced by a canonical JSON Schema document.
//
// It derives the argument schema from I exactly as the SDK does, so every other
// property keeps the constraints its Go type and jsonschema tags describe, then
// substitutes canonical for the named property. The result is suitable for
// [Tool.InputSchema] and [ToolWithRequest.InputSchema].
//
// Use it when a property carries a strict, versioned document the Go type cannot
// express — such as a recursive, closed Automation definition — so the SDK
// validates that document against the canonical schema before the handler runs
// and tools/list advertises the same schema. Reusing the canonical document
// keeps one schema authority rather than a hand-maintained MCP copy.
//
// property must name a property the derived schema declares.
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
