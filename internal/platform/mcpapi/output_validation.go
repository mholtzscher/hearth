package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

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
	_, contract, err := compileObjectContract(outputSuccessSchemaURI, raw)
	if err != nil {
		return nil, fmt.Errorf("prepare output schema: %w", err)
	}
	return contract, nil
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
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("tool output must be a JSON object")
	}
	if err = schema.Validate(value); err != nil {
		return nil, fmt.Errorf("validate tool output: %w", err)
	}
	return encoded, nil
}
