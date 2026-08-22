// Package entitytypes provides schema-backed typed values for Hearth Entity types.
package entitytypes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// JSONCodec validates JSON against an authoritative schema and converts it to T.
type JSONCodec[T any] struct {
	schemaID string
	schema   *jsonschema.Schema
	validate func(T) error
}

// CompileJSONCodec compiles schema once for typed decoding and encoding.
func CompileJSONCodec[T any](schemaID string, schema json.RawMessage, validate func(T) error) (*JSONCodec[T], error) {
	if schemaID == "" {
		return nil, fmt.Errorf("schema ID is required")
	}
	if len(schema) == 0 {
		return nil, fmt.Errorf("schema %q is required", schemaID)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, fmt.Errorf("decode schema %q: %w", schemaID, err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaID, document); err != nil {
		return nil, fmt.Errorf("add schema %q: %w", schemaID, err)
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		return nil, fmt.Errorf("compile schema %q: %w", schemaID, err)
	}
	return &JSONCodec[T]{schemaID: schemaID, schema: compiled, validate: validate}, nil
}

// Decode validates one JSON value, decodes T, and returns its normalized JSON.
func (codec *JSONCodec[T]) Decode(raw json.RawMessage) (T, json.RawMessage, error) {
	var zero T
	if codec == nil || codec.schema == nil {
		return zero, nil, fmt.Errorf("JSON codec is nil")
	}
	value, err := decodeOne(raw)
	if err != nil {
		return zero, nil, fmt.Errorf("decode %q: %w", codec.schemaID, err)
	}
	if err := codec.schema.Validate(value); err != nil {
		return zero, nil, fmt.Errorf("validate %q: %w", codec.schemaID, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var typed T
	if err := decoder.Decode(&typed); err != nil {
		return zero, nil, fmt.Errorf("decode binding for %q: %w", codec.schemaID, err)
	}
	if codec.validate != nil {
		if err := codec.validate(typed); err != nil {
			return zero, nil, fmt.Errorf("validate binding for %q: %w", codec.schemaID, err)
		}
	}
	normalized, err := json.Marshal(typed)
	if err != nil {
		return zero, nil, fmt.Errorf("encode binding for %q: %w", codec.schemaID, err)
	}
	normalizedValue, err := decodeOne(normalized)
	if err != nil {
		return zero, nil, fmt.Errorf("decode normalized binding for %q: %w", codec.schemaID, err)
	}
	if err := codec.schema.Validate(normalizedValue); err != nil {
		return zero, nil, fmt.Errorf("binding for %q does not preserve its schema: %w", codec.schemaID, err)
	}
	return typed, normalized, nil
}

// Encode validates value and returns its normalized JSON representation.
func (codec *JSONCodec[T]) Encode(value T) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode binding: %w", err)
	}
	_, normalized, err := codec.Decode(raw)
	return normalized, err
}

func decodeOne(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("JSON value is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}
