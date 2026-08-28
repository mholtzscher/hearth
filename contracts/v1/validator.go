package v1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Validator validates wire messages against the embedded authoritative schemas.
type Validator struct {
	schemas map[string]*jsonschema.Schema
}

// Compile compiles every embedded v1 schema into a validator.
func Compile() (*Validator, error) {
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	files := SchemaFiles()
	for schemaID, path := range files {
		raw, err := FS.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read schema %q: %w", schemaID, err)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("decode schema %q: %w", schemaID, err)
		}
		if addErr := compiler.AddResource(schemaID, document); addErr != nil {
			return nil, fmt.Errorf("add schema %q: %w", schemaID, addErr)
		}
	}

	schemas := make(map[string]*jsonschema.Schema, len(files))
	for schemaID := range files {
		schema, err := compiler.Compile(schemaID)
		if err != nil {
			return nil, fmt.Errorf("compile schema %q: %w", schemaID, err)
		}
		schemas[schemaID] = schema
	}
	return &Validator{schemas: schemas}, nil
}

// Validate checks that payload is one JSON value conforming to schemaID.
func (validator *Validator) Validate(schemaID string, payload []byte) error {
	schema, ok := validator.schemas[schemaID]
	if !ok {
		return fmt.Errorf("unknown schema %q", schemaID)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode message: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode message: multiple JSON values")
		}
		return fmt.Errorf("decode message: %w", err)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("validate %q: %w", schemaID, err)
	}
	return nil
}
