package mcpapi

import (
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// portableOutputSchema returns the JSON Schema a typed Tool advertises for its
// result, or nil when the SDK advertises none.
//
// The SDK derives an output schema from the success type alone, but this
// wrapper also publishes a structured failure object in the result's
// StructuredContent whenever a handler returns a [ToolError]. An output schema
// that described only success would therefore be violated by every domain
// failure, because the SDK infers "additionalProperties: false" for a struct
// and the failure object adds failure_code, message, and details.
//
// The advertised schema is the union of both shapes: the SDK's success schema
// and the structured failure object. A success result and a structured failure
// result each validate against it, and the union still describes the success
// payload a typed client decodes. The union is an "anyOf" rather than an
// "oneOf" because a tool whose success schema is itself an open object would
// otherwise fail its own successful outputs when they also matched the failure
// branch. Tools whose output type is any advertise no output schema, exactly as
// the SDK does, so nothing constrains their content.
//
// The union carries a top-level "type": "object" because strict clients (the
// Inspector, the Pi MCP gateway) require an output schema to be an object
// schema and drop tools whose root has no object type. Every tool output is a
// body struct and the failure shape is an object, so the conjunction keeps
// exact semantics. A non-object success shape leaves the union unpinned rather
// than advertising a schema its own outputs would violate.
//
// The union is normalized by [portableSchema] because the SDK derives nullable
// members as array-valued `type` unions and unconstrained members as boolean
// schemas, neither of which every client can read.
func portableOutputSchema[O any]() any {
	if reflect.TypeFor[O]() == reflect.TypeFor[any]() {
		return nil
	}
	success := successSchema[O]()
	if success == nil {
		// The SDK derives and resolves the same schema in AddTool. If derivation
		// fails here it fails there too, and AddTool reports it; returning nil
		// keeps that one error message authoritative.
		return nil
	}
	union := &jsonschema.Schema{AnyOf: []*jsonschema.Schema{success, toolErrorSchema()}}
	if success.Type == "" || success.Type == schemaTypeObject {
		union.Type = schemaTypeObject
	}
	return portableSchema(union)
}

// successSchema derives the schema of O the way the SDK does: a pointer output
// type describes its element, because the SDK schema-checks the marshalled
// value rather than the Go type.
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
