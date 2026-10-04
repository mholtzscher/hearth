package mcpapi

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// portableOutputSchema returns the JSON Schema a typed Tool advertises for its
// result, or nil when the SDK advertises none. The advertised schema is the
// union of the SDK's success schema and the structured failure object, so both
// a success result and a [ToolError] result validate against it.
//
// The union is necessary because the SDK derives the output schema from the
// success type alone, but this wrapper also publishes a structured failure
// object whenever a handler returns a [ToolError]. A success-only schema would
// be violated by every domain failure, since the SDK infers
// "additionalProperties: false" for a struct and the failure object adds
// failure_code, message, and details.
//
// It is an "anyOf" rather than an "oneOf" because a tool whose success schema
// is itself an open object would otherwise fail its own successful outputs when
// they also matched the failure branch. The union carries a top-level
// "type": "object" because strict clients (the Inspector, the Pi MCP gateway)
// require an output schema to be an object schema and drop tools whose root has
// no object type; a non-object success shape leaves the union unpinned rather
// than advertising a schema its own outputs would violate. Tools whose output
// type is any advertise no output schema, exactly as the SDK does.
//
// The union is normalized by [portableSchema] because the SDK derives nullable
// members as array-valued `type` unions and unconstrained members as boolean
// schemas, neither of which every client can read.
func portableOutputSchema[O any](override any) any {
	if override != nil {
		return overriddenOutputSchema(override)
	}
	if reflect.TypeFor[O]() == reflect.TypeFor[any]() {
		return nil
	}
	success := successSchema[O]()
	if success == nil {
		// The SDK derives and resolves the same schema in AddTool, which reports
		// the same failure; returning nil keeps that one message authoritative.
		return nil
	}
	union := &jsonschema.Schema{AnyOf: []*jsonschema.Schema{success, toolErrorSchema()}}
	if success.Type == "" || success.Type == schemaTypeObject {
		union.Type = schemaTypeObject
	}
	return portableSchema(union)
}

// overriddenOutputSchema owns a copy before relocating local references into
// the success branch. Only schema keywords are visited, never instance data.
func overriddenOutputSchema(override any) any {
	success, err := normalizePortableSchema(override)
	if err != nil {
		panic(fmt.Errorf("normalize output schema: %w", err))
	}
	rebaseOutputSchema(success)
	return map[string]any{
		"type":  "object",
		"anyOf": []any{success, portableSchema(toolErrorSchema())},
	}
}

func rebaseOutputSchema(node any) {
	schema, ok := node.(map[string]any)
	if !ok {
		return
	}
	// An embedded resource retains its own reference base when relocated.
	if id, hasID := schema["$id"].(string); hasID && id != "" {
		return
	}
	for _, keyword := range []string{"$ref", "$dynamicRef"} {
		if ref, hasRef := schema[keyword].(string); hasRef {
			if ref == "#" {
				schema[keyword] = "#/anyOf/0"
			} else if suffix, local := strings.CutPrefix(ref, "#/"); local {
				schema[keyword] = "#/anyOf/0/" + suffix
			}
		}
	}
	for keyword, child := range schema {
		switch schemaKeywordShape(keyword) {
		case schemaValueSubschema:
			rebaseOutputSchema(child)
		case schemaValueSubschemaArray, schemaValueItems:
			rebaseOutputSchemaItems(child)
		case schemaValueSubschemaMap, schemaValueDependencies:
			rebaseOutputSchemaMap(child)
		case schemaValueOther:
		}
	}
}

func rebaseOutputSchemaItems(child any) {
	if children, array := child.([]any); array {
		for _, item := range children {
			rebaseOutputSchema(item)
		}
		return
	}
	rebaseOutputSchema(child)
}

func rebaseOutputSchemaMap(child any) {
	if children, object := child.(map[string]any); object {
		for _, item := range children {
			rebaseOutputSchema(item)
		}
	}
}

// successSchema derives the schema of O the way the SDK does: a pointer output
// type describes its element, because the SDK schema-checks the marshalled value
// rather than the Go type.
func successSchema[O any]() *jsonschema.Schema {
	outputType := reflect.TypeFor[O]()
	if outputType.Kind() == reflect.Pointer {
		outputType = outputType.Elem()
	}
	schema, err := jsonschema.ForType(outputType, &jsonschema.ForOptions{})
	if err != nil {
		return nil
	}
	return schema
}

// toolErrorSchema describes one structured domain failure: the stable failure
// code and human summary every [ToolError] publishes. Additional properties
// stay permitted so a Code's Details fields validate beside them.
func toolErrorSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: schemaTypeObject,
		Properties: map[string]*jsonschema.Schema{
			toolErrorCodeField:    {Type: schemaTypeString},
			toolErrorMessageField: {Type: schemaTypeString},
		},
		Required: []string{toolErrorCodeField, toolErrorMessageField},
	}
}
