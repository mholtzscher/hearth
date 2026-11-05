package mcpapi_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

// TestPortableInputSchemaPreservesValidation proves the rewrite is
// semantics-preserving: for each schema shape it touches, the schema a client
// receives accepts exactly the argument documents the schema it replaced
// accepted.
//
// Every case declares its expected outcome, and the test checks the original as
// well as the rewritten document against it, so a case that lost its
// discriminating power fails instead of passing vacuously.
func TestPortableInputSchemaPreservesValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		schema    string
		instances []portabilityInstance
	}{
		{
			name:   "empty schemas expand without rewriting instance-valued objects",
			schema: `{"type":"object","properties":{"subject":{},"fixed":{"const":{}}},"required":["subject","fixed"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null,"fixed":{}}`, valid: true},
				{value: `{"subject":[1,"two"],"fixed":{}}`, valid: true},
				{value: `{"subject":{},"fixed":1}`, valid: false},
				{value: `{"subject":true,"fixed":{"extra":1}}`, valid: false},
			},
		},
		{
			name:   "unconstrained JSON value stays unconstrained",
			schema: `{"type":"object","properties":{"subject":true},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":false}`, valid: true},
				{value: `{"subject":7}`, valid: true},
				{value: `{"subject":"text"}`, valid: true},
				{value: `{"subject":[1,"two"]}`, valid: true},
				{value: `{"subject":{"nested":{}}}`, valid: true},
				{value: `{}`, valid: false},
				{value: `{"subject":1,"extra":1}`, valid: false},
			},
		},
		{
			name:   "nullable string keeps its constraints",
			schema: `{"type":"object","properties":{"subject":{"type":["null","string"],"minLength":3,"maxLength":5,"pattern":"^a"}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":"abc"}`, valid: true},
				{value: `{"subject":"abcde"}`, valid: true},
				{value: `{"subject":"ab"}`, valid: false},
				{value: `{"subject":"abcdef"}`, valid: false},
				{value: `{"subject":"bcde"}`, valid: false},
				{value: `{"subject":42}`, valid: false},
			},
		},
		{
			name:   "nullable array keeps its items and count limits",
			schema: `{"type":"object","properties":{"subject":{"type":["null","array"],"items":{"type":"integer","minimum":0},"minItems":1,"maxItems":2}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":[0]}`, valid: true},
				{value: `{"subject":[0,1]}`, valid: true},
				{value: `{"subject":[]}`, valid: false},
				{value: `{"subject":[0,1,2]}`, valid: false},
				{value: `{"subject":[-1]}`, valid: false},
				{value: `{"subject":["zero"]}`, valid: false},
				{value: `{"subject":"zero"}`, valid: false},
			},
		},
		{
			name:   "nullable object keeps its closed shape",
			schema: `{"type":"object","properties":{"subject":{"type":["null","object"],"properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":{"name":"entry"}}`, valid: true},
				{value: `{"subject":{}}`, valid: false},
				{value: `{"subject":{"name":"entry","extra":1}}`, valid: false},
				{value: `{"subject":"entry"}`, valid: false},
			},
		},
		{
			name:   "always-false member stays unsatisfiable",
			schema: `{"type":"object","properties":{"subject":false},"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{}`, valid: true},
				{value: `{"subject":1}`, valid: false},
				{value: `{"subject":null}`, valid: false},
				{value: `{"other":1}`, valid: false},
			},
		},
		{
			name:   "multi-member union splits scoped constraints",
			schema: `{"type":"object","properties":{"subject":{"type":["string","integer","null"],"minLength":2,"minimum":10}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":"ab"}`, valid: true},
				{value: `{"subject":10}`, valid: true},
				{value: `{"subject":10.0}`, valid: true},
				{value: `{"subject":"a"}`, valid: false},
				{value: `{"subject":9}`, valid: false},
				{value: `{"subject":10.5}`, valid: false},
				{value: `{"subject":true}`, valid: false},
			},
		},
		{
			name:   "existing anyOf constraint survives a type union rewrite",
			schema: `{"type":"object","properties":{"subject":{"type":["null","string"],"anyOf":[{"const":"ok"}]}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":"ok"}`, valid: true},
				{value: `{"subject":"other"}`, valid: false},
				{value: `{"subject":null}`, valid: false},
				{value: `{"subject":3}`, valid: false},
			},
		},
		{
			name:   "union nested behind a ref keeps resolving",
			schema: `{"type":"object","$defs":{"leaf":{"type":["null","string"],"minLength":2}},"properties":{"subject":{"$ref":"#/$defs/leaf"}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":"ab"}`, valid: true},
				{value: `{"subject":"a"}`, valid: false},
				{value: `{"subject":5}`, valid: false},
			},
		},
		{
			name:   "union in an embedded-id document keeps its base",
			schema: `{"type":"object","properties":{"subject":{"type":["null","object"],"$id":"urn:hearth:portability:embedded","$defs":{"name":{"type":"string","minLength":2}},"properties":{"name":{"$ref":"#/$defs/name"}},"required":["name"],"additionalProperties":false}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":{"name":"ab"}}`, valid: true},
				{value: `{"subject":{"name":"a"}}`, valid: false},
				{value: `{"subject":{}}`, valid: false},
				{value: `{"subject":{"name":"ab","extra":1}}`, valid: false},
			},
		},
		{
			name:   "default on a nullable member still validates",
			schema: `{"type":"object","properties":{"subject":{"type":["null","string"],"default":"fallback","minLength":2}},"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{}`, valid: true},
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":"ab"}`, valid: true},
				{value: `{"subject":"a"}`, valid: false},
				{value: `{"subject":3}`, valid: false},
			},
		},
		{
			name:   "boolean under a boolean-valued keyword stays a keyword value",
			schema: `{"type":"object","properties":{"subject":{"type":"object","additionalProperties":true}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":{}}`, valid: true},
				{value: `{"subject":{"anything":1}}`, valid: true},
				{value: `{"subject":1}`, valid: false},
			},
		},
		{
			name:   "instance values inside enum survive the rewrite",
			schema: `{"type":"object","properties":{"subject":{"type":["string","boolean"],"enum":["auto",true]}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":"auto"}`, valid: true},
				{value: `{"subject":true}`, valid: true},
				{value: `{"subject":false}`, valid: false},
				{value: `{"subject":"manual"}`, valid: false},
				{value: `{"subject":1}`, valid: false},
			},
		},
	}

	// The matrix belongs to the pure rewrite owner; registered type hooks in
	// portable_schema_integration_test.go prove production publishes that rewrite.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			advertised, normalizeErr := mcpapi.NormalizeSchemaForTest(json.RawMessage(test.schema))
			if normalizeErr != nil {
				t.Fatal(normalizeErr)
			}
			if findings := portableSchemaFindings(advertised); len(findings) != 0 {
				t.Fatalf("advertised schema is not portable:\n%s", strings.Join(findings, "\n"))
			}
			advertisedJSON, err := json.Marshal(advertised)
			if err != nil {
				t.Fatalf("marshal advertised schema: %v", err)
			}
			for _, instance := range test.instances {
				assertSchemaAcceptance(t, "original "+test.name, test.schema, instance)
				assertSchemaAcceptance(t, "portable "+test.name, string(advertisedJSON), instance)
			}
		})
	}
}

func assertSchemaAcceptance(t *testing.T, label, schema string, instance portabilityInstance) {
	t.Helper()
	if accepted := schemaAccepts(t, label, schema, instance.value); accepted != instance.valid {
		t.Fatalf("%s accepted %s = %t, want %t", label, instance.value, accepted, instance.valid)
	}
}

// TestPortableInputSchemaIsIdempotent proves the rewrite is a fixed point: a
// schema that already passed through it is republished unchanged, so a tool
// registered from another tool's advertised schema cannot drift further.
func TestPortableInputSchemaIsIdempotent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		schema string
	}{
		{name: "override document", schema: portabilityOverrideSchema},
		{
			name: "union with scoped constraints",
			schema: `{
  "type": "object",
  "properties": {
    "subject": {"type": ["null", "array"], "items": {"type": "string"}, "minItems": 1}
  },
  "required": ["subject"],
  "additionalProperties": false
}`,
		},
	}
	for _, test := range tests {
		first, err := mcpapi.NormalizeSchemaForTest(json.RawMessage(test.schema))
		if err != nil {
			t.Fatal(err)
		}
		second, err := mcpapi.NormalizeSchemaForTest(first)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("%s is not a fixed point:\nfirst:  %#v\nsecond: %#v", test.name, first, second)
		}
	}
}
