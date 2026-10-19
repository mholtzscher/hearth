package functionschema_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/contracts/functionschema"
)

func TestCompileRejectsUnsupportedAndMalformedSchemas(t *testing.T) {
	t.Parallel()
	for name, document := range map[string]string{
		"missing object root":  `{}`,
		"nullable root":        `{"type":["object","null"]}`,
		"reference":            `{"type":"object","$ref":"https://invalid.example/schema"}`,
		"nested reference":     `{"type":"object","properties":{"x":{"$ref":"file:///etc/passwd"}}}`,
		"definitions":          `{"type":"object","$defs":{}}`,
		"id":                   `{"type":"object","$id":"urn:custom"}`,
		"composition":          `{"type":"object","allOf":[]}`,
		"regex":                `{"type":"object","properties":{"x":{"type":"string","pattern":".*"}}}`,
		"format":               `{"type":"object","properties":{"x":{"format":"email"}}}`,
		"defaults":             `{"type":"object","properties":{"x":{"default":1}}}`,
		"draft":                `{"type":"object","$schema":"http://json-schema.org/draft-07/schema#"}`,
		"tuple":                `{"type":"object","items":[{"type":"number"}]}`,
		"invalid keyword type": `{"type":"object","required":"x"}`,
		"invalid constraint":   `{"type":"object","multipleOf":0}`,
		"negative constraint":  `{"type":"object","maxItems":-1}`,
		"fractional length":    `{"type":"object","minLength":0.5}`,
		"unknown type":         `{"type":"object","properties":{"x":{"type":"date"}}}`,
		"duplicate keyword":    `{"type":"object","type":"object"}`,
		"escaped duplicate":    `{"type":"object","properties":{"x":{},"\u0078":{}}}`,
		"duplicate data":       `{"type":"object","const":{"x":1,"x":2}}`,
		"trailing":             `{"type":"object"} {}`,
		"invalid utf8":         "{\"type\":\"object\",\"description\":\"\xff\"}",
		"annotation limit":     `{"type":"object","description":"` + strings.Repeat("界", 2001) + `"}`,
		"schema bytes":         `{"type":"object","const":{"x":"` + strings.Repeat("x", 16384) + `"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := functionschema.Compile(json.RawMessage(document))
			if !errors.Is(err, functionschema.ErrInvalidSchema) {
				t.Fatalf("Compile error = %v", err)
			}
		})
	}
}

func TestCompiledSchemaValidatesProfileAndPreservesNumbers(t *testing.T) {
	t.Parallel()
	schema, err := functionschema.Compile(json.RawMessage(`{
		"type":"object", "additionalProperties":false, "required":["n","values","nullable"],
		"properties":{
			"n":{"type":"integer","const":9007199254740993},
			"values":{"type":"array","minItems":1,"maxItems":2,"uniqueItems":true,
				"items":{"type":"number","minimum":0,"exclusiveMaximum":5,"multipleOf":0.5}},
			"nullable":{"type":["string","null"],"minLength":1,"maxLength":3}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		`{"n":9007199254740993,"values":[1.0,2e0],"nullable":null}`,
		`{"nullable":"界界界","values":[0],"n":9007199254740993}`,
	} {
		if err = schema.Validate(json.RawMessage(value)); err != nil {
			t.Fatalf("valid object: %v", err)
		}
	}
	normalized, err := schema.Normalize(json.RawMessage(` {"values":[1.0,2e0],"nullable":null,"n":9007199254740993} `))
	if err != nil || string(normalized) != `{"n":9007199254740993,"nullable":null,"values":[1.0,2e0]}` {
		t.Fatalf("normalized = %s, error = %v", normalized, err)
	}
	for _, value := range []string{
		`{"n":9007199254740992,"values":[1],"nullable":null}`,
		`{"n":9007199254740993,"values":[1,1.0],"nullable":null}`,
		`{"n":9007199254740993,"values":[0.1],"nullable":null}`,
		`{"n":9007199254740993,"values":[5],"nullable":null}`,
		`{"n":9007199254740993,"values":[],"nullable":null}`,
		`{"n":9007199254740993,"values":[1],"nullable":""}`,
		`{"n":9007199254740993,"values":[1],"nullable":"界界界界"}`,
		`{"n":9007199254740993,"values":[1]}`,
		`{"n":9007199254740993,"values":[1],"nullable":null,"secret":"not accepted"}`,
	} {
		if err = schema.Validate(json.RawMessage(value)); !errors.Is(err, functionschema.ErrInvalidValue) {
			t.Fatalf("invalid object accepted: %s, error %v", value, err)
		}
	}
}

func TestSchemaKeywordNamesInsideDataAreNotAssertions(t *testing.T) {
	t.Parallel()
	schema, err := functionschema.Compile(json.RawMessage(`{
		"type":"object","properties":{"$ref":{"const":{"$defs":1,"default":2}},
		"enum":{"enum":[{"allOf":3}]}},"additionalProperties":{"type":"integer"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(
		json.RawMessage(`{"$ref":{"$defs":1,"default":2},"enum":{"allOf":3},"extra":4}`),
	); err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(
		json.RawMessage(`{"extra":"not an integer"}`),
	); !errors.Is(
		err,
		functionschema.ErrInvalidValue,
	) {
		t.Fatalf("additionalProperties schema not enforced: %v", err)
	}
}

func TestSchemaBoundsAndOwnedDocument(t *testing.T) {
	t.Parallel()
	for _, count := range []int{255, 256} {
		properties := make(map[string]any, count)
		for index := range count {
			properties[fmt.Sprintf("p%d", index)] = map[string]any{"type": "number"}
		}
		document, _ := json.Marshal(map[string]any{"type": "object", "properties": properties})
		_, err := functionschema.Compile(document)
		if (count == 255) != (err == nil) {
			t.Fatalf("%d children: %v", count, err)
		}
	}
	for _, count := range []int{64, 65} {
		items := make([]int, count)
		for index := range count {
			items[index] = index
		}
		document, _ := json.Marshal(
			map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"enum": items}}},
		)
		_, err := functionschema.Compile(document)
		if (count == 64) != (err == nil) {
			t.Fatalf("enum count %d: %v", count, err)
		}
	}
	for _, depth := range []int{16, 17} {
		child := `{"type":"number"}`
		for index := 1; index < depth; index++ {
			child = `{"type":"object","properties":{"x":` + child + `}}`
		}
		_, err := functionschema.Compile(json.RawMessage(child))
		if (depth == 16) != (err == nil) {
			t.Fatalf("schema depth %d: %v", depth, err)
		}
	}
	document := json.RawMessage(`{"type":"object","properties":{"x":{"const":1.0}}}`)
	schema, err := functionschema.Compile(document)
	if err != nil {
		t.Fatal(err)
	}
	document[0] = '['
	owned := schema.Document()
	owned[0] = '['
	if got := string(schema.Document()); got != `{"properties":{"x":{"const":1.0}},"type":"object"}` {
		t.Fatalf("schema document mutated: %s", got)
	}
}
