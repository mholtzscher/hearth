package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// compileOutputSuccess keeps successful output validation separate from the
// advertised union, whose failure branch is reserved for returned ToolErrors.
func compileOutputSuccess(schema any, objectRoot bool) *jsonschema.Schema {
	if schema == nil {
		return nil
	}
	if objectRoot {
		document, normalizeErr := normalizePortableSchema(schema)
		if normalizeErr != nil {
			panic(fmt.Errorf("normalize output success schema: %w", normalizeErr))
		}
		// Append at the root to preserve every local reference's location.
		constraints, _ := document["allOf"].([]any)
		document["allOf"] = append(constraints, map[string]any{"type": "object"})
		schema = document
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Errorf("marshal output success schema: %w", err))
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		panic(fmt.Errorf("decode output success schema: %w", err))
	}
	compiler := jsonschema.NewCompiler()
	const schemaURI = "https://hearth.invalid/mcp-output-success.json"
	if err = compiler.AddResource(schemaURI, document); err != nil {
		panic(fmt.Errorf("load output success schema: %w", err))
	}
	resolved, err := compiler.Compile(schemaURI)
	if err != nil {
		panic(fmt.Errorf("resolve output success schema: %w", err))
	}
	return resolved
}

// encodeValidatedOutput validates a decoded copy, retaining original bytes for
// exact output. A nil untyped output is JSON null and must pass the same contract.
func encodeValidatedOutput(output any, schema *jsonschema.Schema) (json.RawMessage, error) {
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("marshal tool output: %w", err)
	}
	if schema != nil {
		value, decodeErr := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
		if decodeErr != nil {
			return nil, fmt.Errorf("decode tool output: %w", decodeErr)
		}
		if err = schema.Validate(value); err != nil {
			return nil, fmt.Errorf("validate tool output: %w", err)
		}
	}
	return encoded, nil
}
