package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	googleschema "github.com/google/jsonschema-go/jsonschema"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const outputSuccessSchemaURI = "https://hearth.invalid/mcp-output-success.json"

// compileSuccessSchema compiles the actual marshaled success value's contract.
// Explicit contracts require an object root; embedded resource IDs and local
// references retain their original locations and resolution bases.
func compileSuccessSchema[O any](schema any) (*jsonschema.Schema, error) {
	explicit := schema != nil
	if !explicit {
		outputType := reflect.TypeFor[O]()
		if outputType == reflect.TypeFor[any]() {
			return nil, fmt.Errorf("untyped output requires an explicit success schema")
		}
		if outputType.Kind() == reflect.Pointer {
			outputType = outputType.Elem()
		}
		derived, err := googleschema.ForType(outputType, &googleschema.ForOptions{})
		if err != nil {
			return nil, fmt.Errorf("derive output schema: %w", err)
		}
		schema = derived
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal output schema: %w", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode output schema: %w", err)
	}
	if explicit {
		_, ok := document.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("output schema must be a JSON object")
		}
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(blockExternalSchemas{})
	if err = compiler.AddResource(outputSuccessSchemaURI, document); err != nil {
		return nil, fmt.Errorf("load output schema: %w", err)
	}
	validationURI := outputSuccessSchemaURI
	if explicit {
		// Keep the supplied resource intact. In older drafts, siblings of $ref
		// are ignored, so adding an object constraint beside it is insufficient.
		validationURI += ".object"
		root := map[string]any{"allOf": []any{
			map[string]any{"type": "object"}, map[string]any{"$ref": outputSuccessSchemaURI},
		}}
		if err = compiler.AddResource(validationURI, root); err != nil {
			return nil, fmt.Errorf("load output root constraint: %w", err)
		}
	}
	validator, err := compiler.Compile(validationURI)
	if err != nil {
		return nil, fmt.Errorf("resolve output schema: %w", err)
	}
	return validator, nil
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
