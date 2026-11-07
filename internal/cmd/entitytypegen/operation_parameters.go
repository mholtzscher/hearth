package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

func operationParametersJSONPath(root string) string {
	return filepath.Join(root, "contracts", "operation-parameters", "v1", "operation-parameters.json")
}

// operationParametersOutput publishes the original schemas, not support-dependent
// rules or an approximation of the schemaNode fields understood by the generator.
func operationParametersOutput(root string, models []entityTypeModel) (output, error) {
	type operation struct {
		ParametersSchema json.RawMessage `json:"parameters_schema"`
	}
	type entityType struct {
		Operations map[string]operation `json:"operations"`
	}
	document := struct {
		Comment       string                `json:"$comment"`
		SchemaVersion int                   `json:"schema_version"`
		EntityTypes   map[string]entityType `json:"entity_types"`
	}{
		Comment:       strings.TrimSuffix(strings.TrimPrefix(generatedPrefix, "// "), "\n"),
		SchemaVersion: 1,
		EntityTypes:   make(map[string]entityType, len(models)),
	}
	for _, model := range models {
		operations := make(map[string]operation, len(model.Operations))
		for _, item := range model.Operations {
			operations[item.Name] = operation{ParametersSchema: item.ParametersSchema.Raw}
		}
		document.EntityTypes[model.TypeID] = entityType{Operations: operations}
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return output{}, fmt.Errorf("encode operation parameters: %w", err)
	}
	return output{path: operationParametersJSONPath(root), content: encoded.Bytes()}, nil
}
