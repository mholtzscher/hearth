// Package functionschema implements Hearth's bounded dynamic JSON Schema profile.
package functionschema

import (
	"encoding/json"
	"errors"
	"slices"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	MaxSchemaBytes      = 16 * 1024
	maxSchemaNodes      = 256
	maxSchemaDepth      = 16
	maxSchemaListItems  = 64
	maxAnnotationLength = 2000
	draftURI            = "https://json-schema.org/draft/2020-12/schema"
	resourceURI         = "urn:hearth:function-schema:v1"
)

var (
	ErrInvalidSchema = errors.New("invalid function schema")
	ErrInvalidValue  = errors.New("invalid function value")
)

// Schema is immutable and safe for concurrent validation.
type Schema struct {
	compiled *jsonschema.Schema
	document json.RawMessage
}

// Compile rejects unsupported assertions before compiling the bounded profile.
// Errors intentionally omit validator excerpts, which may contain payload data.
func Compile(document json.RawMessage) (*Schema, error) {
	// Each schema nesting step can introduce both a properties map and a schema
	// object. Literal const/enum data has the same container bound as values.
	object, err := decodeObject(document, maxSchemaDepth*2+MaxValueDepth)
	if err != nil || object["type"] != "object" {
		return nil, ErrInvalidSchema
	}
	normalized, err := json.Marshal(object)
	if err != nil || len(normalized) > MaxSchemaBytes {
		return nil, ErrInvalidSchema
	}
	nodes := 0
	if err = checkProfile(object, 1, &nodes); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(rejectLoader{})
	if err = compiler.AddResource(resourceURI, object); err != nil {
		return nil, ErrInvalidSchema
	}
	compiled, err := compiler.Compile(resourceURI)
	if err != nil {
		return nil, ErrInvalidSchema
	}
	return &Schema{compiled: compiled, document: normalized}, nil
}

// Document returns an owned normalized schema for catalog comparison/storage.
// It reuses compilation's preparation rather than decoding the schema again.
func (schema *Schema) Document() json.RawMessage {
	return slices.Clone(schema.document)
}

// Validate enforces both value bounds and the compiled schema.
func (schema *Schema) Validate(document json.RawMessage) error {
	_, err := schema.Normalize(document)
	return err
}

// Normalize validates an object and returns its normalized bytes in one pass.
// Callers retaining validated values can reuse this encoding without reparsing.
func (schema *Schema) Normalize(document json.RawMessage) (json.RawMessage, error) {
	if schema == nil || schema.compiled == nil {
		return nil, ErrInvalidSchema
	}
	value, err := decodeObject(document, MaxValueDepth)
	if err != nil {
		return nil, ErrInvalidValue
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > MaxValueBytes {
		return nil, ErrInvalidValue
	}
	if err = schema.compiled.Validate(value); err != nil {
		return nil, ErrInvalidValue
	}
	return encoded, nil
}

type rejectLoader struct{}

func (rejectLoader) Load(string) (any, error) { return nil, ErrInvalidSchema }

func checkProfile(schema map[string]any, depth int, nodes *int) error {
	*nodes++
	if depth > maxSchemaDepth || *nodes > maxSchemaNodes {
		return ErrInvalidSchema
	}
	for keyword, value := range schema {
		if err := checkKeyword(keyword, value, depth, nodes); err != nil {
			return err
		}
	}
	return nil
}

func checkKeyword(keyword string, value any, depth int, nodes *int) error {
	switch keyword {
	case "$schema":
		if value != draftURI {
			return ErrInvalidSchema
		}
	case "title", "description":
		text, ok := value.(string)
		if !ok || utf8.RuneCountInString(text) > maxAnnotationLength {
			return ErrInvalidSchema
		}
	case "properties":
		properties, ok := value.(map[string]any)
		if !ok {
			return ErrInvalidSchema
		}
		for _, child := range properties {
			if err := checkSubschema(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case "items":
		return checkSubschema(value, depth+1, nodes)
	case "additionalProperties":
		if _, boolean := value.(bool); !boolean {
			return checkSubschema(value, depth+1, nodes)
		}
	case "enum", "required", "type":
		if list, ok := value.([]any); ok && len(list) > maxSchemaListItems {
			return ErrInvalidSchema
		}
	case "minItems", "maxItems", "uniqueItems", "minLength", "maxLength",
		"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "const":
		// The draft metaschema validates standard keyword types and constraints.
	default:
		return ErrInvalidSchema
	}
	return nil
}

func checkSubschema(value any, depth int, nodes *int) error {
	object, ok := value.(map[string]any)
	if !ok {
		return ErrInvalidSchema
	}
	return checkProfile(object, depth, nodes)
}
