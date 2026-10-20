package mcpapi_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// portabilityDerivedInput exercises every schema shape the portability rewrite
// touches in a schema the SDK derives from Go types: an unconstrained JSON value,
// a nullable scalar, a nullable array, a nullable object, and a list of nullable
// objects.
type portabilityDerivedInput struct {
	Free  any                `json:"free,omitempty"`
	Note  *string            `json:"note,omitempty"`
	Tags  []string           `json:"tags,omitempty"`
	Point *portabilityPoint  `json:"point,omitempty"`
	Path  []portabilityPoint `json:"path,omitempty"`
}

type portabilityPoint struct {
	Label    string `json:"label"`
	Priority *int   `json:"priority,omitempty"`
}

// portabilityDerivedOutput mirrors the input shapes on the result side, so the
// advertised success schema is validated against a body carrying an
// unconstrained member and nullable members.
type portabilityDerivedOutput struct {
	Points []portabilityPoint `json:"points"`
	Note   any                `json:"note"`
	Label  *string            `json:"label,omitempty"`
	Target *portabilityPoint  `json:"target,omitempty"`
}

// portabilityOverrideSchema is a caller-supplied argument schema carrying every
// shape the rewrite replaces: a bare `true` and a bare `false`, array-valued
// `type` unions inside a `$id`-scoped document with `$defs`, and a `default` on a
// nullable member.
const portabilityOverrideSchema = `{
  "type": "object",
  "properties": {
    "definition": {
      "type": "object",
      "$id": "urn:hearth:portability:override",
      "title": "Portability override",
      "$defs": {
        "leaf": {
          "type": ["null", "object"],
          "properties": {"operand": true},
          "required": ["operand"],
          "additionalProperties": false
        }
      },
      "properties": {
        "operand": true,
        "note": {"type": ["null", "string"], "default": "none"},
        "condition": {"$ref": "#/$defs/leaf"},
        "variants": {
          "type": ["null", "array"],
          "minItems": 1,
          "items": {"oneOf": [{"type": "string"}, false]}
        }
      },
      "required": ["operand"],
      "additionalProperties": false
    }
  },
  "required": ["definition"],
  "additionalProperties": false
}`

// TestPortableSchemaFindingsDetectNonPortableSchemas is the negative control for
// the checker the portability tests rely on: it must report the shapes the
// rewrite removes and must not report a boolean that is a keyword value or an
// instance rather than a schema.
func TestPortableSchemaFindingsDetectNonPortableSchemas(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		schema    string
		wantFound bool
	}{
		{
			name:      "bare true in a property position",
			schema:    `{"type":"object","properties":{"value":true}}`,
			wantFound: true,
		},
		{
			name:      "bare false in a property position",
			schema:    `{"type":"object","properties":{"value":false}}`,
			wantFound: true,
		},
		{
			name:      "array-valued type",
			schema:    `{"type":"object","properties":{"value":{"type":["null","string"]}}}`,
			wantFound: true,
		},
		{
			name:      "boolean under a boolean-valued keyword",
			schema:    `{"type":"object","properties":{"value":{"type":"object","readOnly":true,"properties":{"tags":{"type":"array","items":{"type":"string"},"uniqueItems":true}}}}}`,
			wantFound: false,
		},
		{
			name:      "boolean inside an instance-valued keyword",
			schema:    `{"type":"object","properties":{"value":{"enum":["auto",true,1]}}}`,
			wantFound: false,
		},
		{
			name:      "already portable union",
			schema:    `{"type":"object","properties":{"value":{"anyOf":[{"type":"null"},{"type":"string"}]}}}`,
			wantFound: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			findings := portableSchemaFindings(decodedJSONValue(t, test.schema))
			if (len(findings) != 0) != test.wantFound {
				t.Fatalf("findings = %v, wantFound = %t", findings, test.wantFound)
			}
		})
	}
}

type portabilityInstance struct {
	value string
	valid bool
}

func wireSchemaValue(t *testing.T, label string, schema any) any {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("%s: marshal schema: %v", label, err)
	}
	var value any
	if decodeErr := json.Unmarshal(raw, &value); decodeErr != nil {
		t.Fatalf("%s: decode schema %s: %v", label, raw, decodeErr)
	}
	return value
}

func decodedJSONValue(t *testing.T, document string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(document), &value); err != nil {
		t.Fatalf("decode %s: %v", document, err)
	}
	return value
}

// schemaAccepts reports whether one argument document validates against one
// argument schema under the validator the SDK itself uses, so an equivalence
// check compares enforcement rather than the wrapper's intent.
func schemaAccepts(t *testing.T, label, schema, instance string) bool {
	t.Helper()
	var document jsonschema.Schema
	if err := json.Unmarshal([]byte(schema), &document); err != nil {
		t.Fatalf("%s: decode schema %s: %v", label, schema, err)
	}
	resolved, err := document.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("%s: resolve schema %s: %v", label, schema, err)
	}
	value := decodedJSONValue(t, instance)
	return resolved.Validate(value) == nil
}

func decodedObject(t *testing.T, label string, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want a JSON object", label, value)
	}
	return object
}

// assertTypeUnion requires node to be an `anyOf` of single-type branches naming
// exactly memberTypes, in order, and no array-valued `type` of its own.
func assertTypeUnion(t *testing.T, label string, node map[string]any, memberTypes []string) {
	t.Helper()
	if _, present := node["type"]; present {
		t.Fatalf("%s keeps an array-valued type: %#v", label, node)
	}
	branches := decodedArray(t, label+" anyOf", node["anyOf"])
	if len(branches) != len(memberTypes) {
		t.Fatalf("%s anyOf has %d branches, want %d", label, len(branches), len(memberTypes))
	}
	for index, branch := range branches {
		object := decodedObject(t, fmt.Sprintf("%s branch %d", label, index), branch)
		if object["type"] != memberTypes[index] {
			t.Fatalf("%s branch %d type = %#v, want %q", label, index, object["type"], memberTypes[index])
		}
	}
}

func anyOfBranch(t *testing.T, label string, node map[string]any, memberType string) map[string]any {
	t.Helper()
	for _, branch := range decodedArray(t, label+" anyOf", node["anyOf"]) {
		object := decodedObject(t, label+" branch", branch)
		if object["type"] == memberType {
			return object
		}
	}
	t.Fatalf("%s has no %q branch: %#v", label, memberType, node)
	return nil
}

func decodedArray(t *testing.T, label string, value any) []any {
	t.Helper()
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want a JSON array", label, value)
	}
	return array
}

// portableSchemaFindings reports every node of one decoded JSON Schema a strict
// MCP client cannot read: a boolean subschema node, or an array-valued `type`.
//
// It mirrors the Inspector's lint, so it does not report a boolean that is a
// keyword value (`uniqueItems`, `readOnly`) and it does not descend into keyword
// values that hold instances rather than schemas (`enum`, `const`, `default`,
// `examples`).
func portableSchemaFindings(schema any) []string {
	// A boolean under one of these keywords is a keyword value or an instance,
	// not a schema: "uniqueItems" and "readOnly" take a boolean, and "enum",
	// "const", "default", and "examples" hold instances.
	booleanValueKeywords := map[string]bool{
		"uniqueItems": true,
		"readOnly":    true,
		"writeOnly":   true,
		"deprecated":  true,
	}
	instanceValueKeywords := map[string]bool{
		"enum":        true,
		"const":       true,
		"default":     true,
		"examples":    true,
		"$vocabulary": true,
	}
	var findings []string
	var walk func(node any, path, keyword string)
	walk = func(node any, path, keyword string) {
		switch value := node.(type) {
		case bool:
			if booleanValueKeywords[keyword] {
				return
			}
			findings = append(findings, fmt.Sprintf("%s is the boolean schema %v", path, value))
		case map[string]any:
			if members, isArray := value["type"].([]any); isArray {
				findings = append(findings, fmt.Sprintf("%s.type is the array %v", path, members))
			}
			for key, child := range value {
				if instanceValueKeywords[key] || booleanValueKeywords[key] {
					continue
				}
				walk(child, path+"."+key, key)
			}
		case []any:
			for index, element := range value {
				walk(element, fmt.Sprintf("%s[%d]", path, index), keyword)
			}
		}
	}
	walk(schema, "", "")
	return findings
}
