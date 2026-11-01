package mcpapi_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"

	"github.com/danielgtaylor/huma/v2"
)

type contractFixture interface {
	document() any
}

// contractOutput lets each test contract belong to a concrete named Go type,
// while exercising the actual marshaled value rather than a reflected proxy.
type contractOutput[S contractFixture] struct{ value any }

func (value contractOutput[S]) MarshalJSON() ([]byte, error) { return json.Marshal(value.value) }

func (contractOutput[S]) Schema(huma.Registry) *huma.Schema {
	var source S
	return fixtureSchema(source.document())
}

type contractInput[S contractFixture] map[string]any

func (contractInput[S]) Schema(huma.Registry) *huma.Schema {
	var source S
	return fixtureSchema(source.document())
}

func fixtureSchema(document any) *huma.Schema {
	raw, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&value); err != nil {
		panic(err)
	}
	return &huma.Schema{Extensions: value}
}

type openObjectContract struct{}

func (openObjectContract) document() any { return json.RawMessage(`{"type":"object"}`) }

type portabilityContract struct{}

func (portabilityContract) document() any { return json.RawMessage(portabilityOverrideSchema) }

type subjectDefaultInput struct {
	Subject *string `json:"subject,omitempty" nullable:"true" default:"fallback"`
}

type requiredDefaultContract struct{}

func (requiredDefaultContract) document() any {
	return json.RawMessage(
		`{"type":"object","properties":{"value":{"type":"integer","default":7}},"required":["value"],"additionalProperties":false}`,
	)
}

type optionalDefaultContract struct{}

func (optionalDefaultContract) document() any {
	return json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer","default":7}}}`)
}

type nullableReferenceContract struct{}

func (nullableReferenceContract) document() any {
	return json.RawMessage(
		`{"$schema":"http://json-schema.org/draft-07/schema#","$ref":"#/definitions/result","definitions":{"result":{"type":["object","null"],"additionalProperties":false}}}`,
	)
}

type missingReferenceContract struct{}

type nonObjectReferenceContract struct{}

func (nonObjectReferenceContract) document() any {
	return json.RawMessage(
		`{"$ref":"#/$defs/scalar","$defs":{"scalar":{"anyOf":[{"type":"integer"},{"type":"null"}]}}}`,
	)
}

func (missingReferenceContract) document() any {
	return json.RawMessage(`{"type":"object","$ref":"#/$defs/missing"}`)
}

type malformedContract struct{}

func (malformedContract) document() any { return json.RawMessage(`{`) }

type unmarshalableContract struct{}

func (unmarshalableContract) document() any { return make(chan int) }

type externalReferenceContract struct{}

func (externalReferenceContract) document() any {
	return map[string]any{"type": "object", "$ref": "https://hearth.test/missing.json"}
}

type fileReferenceContract struct{}

func (fileReferenceContract) document() any {
	path, err := filepath.Abs("testdata/external-schema.json")
	if err != nil {
		panic(err)
	}
	return map[string]any{"type": "object", "$ref": "file://" + path}
}

type recursiveContract struct{}

func (recursiveContract) document() any {
	schema := recursiveSchemaDocument()
	schema["type"] = []string{"object", "null"}
	schema["$defs"].(map[string]any)["node"].(map[string]any)["type"] = []string{"object", "null"}
	return schema
}

type identifiedRecursiveContract struct{}

func (identifiedRecursiveContract) document() any {
	schema := recursiveContract{}.document().(map[string]any)
	schema["$id"] = "https://hearth.test/schemas/tree.json"
	return schema
}

type exactRecursiveContract struct{}

func (exactRecursiveContract) document() any {
	schema := recursiveSchemaDocument()
	schema["properties"] = map[string]any{"value": map[string]any{"const": json.Number("9007199254740993")}}
	return schema
}
