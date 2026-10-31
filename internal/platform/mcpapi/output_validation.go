package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const outputSuccessSchemaURI = "https://hearth.invalid/mcp-output-success.json"

// compileSuccessSchema compiles the actual marshaled success value's contract.
// Results require an object root; embedded resource IDs and local references
// retain their original locations and resolution bases.
func compileSuccessSchema[O any]() (*jsonschema.Schema, error) {
	outputType := reflect.TypeFor[O]()
	if outputType.Kind() == reflect.Interface {
		return nil, fmt.Errorf("tool output requires a concrete type")
	}
	schema, err := deriveToolSchema(outputType)
	if err != nil {
		return nil, fmt.Errorf("derive output schema: %w", err)
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal output schema: %w", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode output schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(blockExternalSchemas{})
	if err = compiler.AddResource(outputSuccessSchemaURI, document); err != nil {
		return nil, fmt.Errorf("load output schema: %w", err)
	}
	contract, err := compiler.Compile(outputSuccessSchemaURI)
	if err != nil {
		return nil, fmt.Errorf("resolve output schema: %w", err)
	}
	if !schemaAllowsObject(contract, nil) {
		return nil, fmt.Errorf("tool output schema cannot accept an object")
	}
	validationURI := outputSuccessSchemaURI + ".object"
	// Keep the supplied resource intact. In older drafts, siblings of $ref
	// are ignored, so adding an object constraint beside it is insufficient.
	root := map[string]any{"allOf": []any{
		map[string]any{"type": "object"}, map[string]any{"$ref": outputSuccessSchemaURI},
	}}
	if err = compiler.AddResource(validationURI, root); err != nil {
		return nil, fmt.Errorf("load output root constraint: %w", err)
	}
	validator, err := compiler.Compile(validationURI)
	if err != nil {
		return nil, fmt.Errorf("resolve output schema: %w", err)
	}
	return validator, nil
}

// schemaAllowsObject rejects explicit non-object root types through references
// and composition. It is a type check, not a general schema satisfiability proof;
// recursive references add no new restriction once already visited.
func schemaAllowsObject(schema *jsonschema.Schema, seen map[*jsonschema.Schema]bool) bool {
	if schema == nil || seen[schema] {
		return true
	}
	if (schema.Bool != nil && !*schema.Bool) ||
		(schema.Types != nil && !slices.Contains(schema.Types.ToStrings(), schemaTypeObject)) {
		return false
	}
	if seen == nil {
		seen = make(map[*jsonschema.Schema]bool)
	}
	seen[schema] = true
	defer delete(seen, schema)
	if !schemaAllowsObject(schema.Ref, seen) {
		return false
	}
	for _, child := range schema.AllOf {
		if !schemaAllowsObject(child, seen) {
			return false
		}
	}
	for _, alternatives := range [][]*jsonschema.Schema{schema.AnyOf, schema.OneOf} {
		if len(alternatives) > 0 && !slices.ContainsFunc(alternatives, func(child *jsonschema.Schema) bool {
			return schemaAllowsObject(child, seen)
		}) {
			return false
		}
	}
	return true
}

type blockExternalSchemas struct{}

func (blockExternalSchemas) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema loading disabled: %s", url)
}

// encodeValidatedOutput validates a decoded copy without applying defaults or
// re-encoding numbers. Only the original encoded bytes reach the client.
func encodeValidatedOutput(output any, schema *jsonschema.Schema) (json.RawMessage, error) {
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("marshal tool output: %w", err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("decode tool output: %w", err)
	}
	if err = schema.Validate(value); err != nil {
		return nil, fmt.Errorf("validate tool output: %w", err)
	}
	return encoded, nil
}
