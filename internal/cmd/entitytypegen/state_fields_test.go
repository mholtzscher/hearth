package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fieldSchema(t *testing.T, raw string) schemaNode {
	t.Helper()
	var node schemaNode
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		t.Fatal(err)
	}
	return node
}

func TestStateFieldsDeriveScalarsAndPresenceWithoutAllowlist(t *testing.T) {
	t.Parallel()
	state := fieldSchema(t, `{"type":"object","required":["group","mode"],"properties":{
 "group":{"type":"object","required":["zero"],"properties":{"zero":{"type":"number"},"flag":{"type":"boolean"}}},
 "mode":{"type":"string","enum":["value","choice"]},"value":{"type":"number"},"choice":{"type":"string"}},
 "oneOf":[{"required":["value"],"not":{"required":["choice"]},"properties":{"mode":{"const":"value"}}},
 {"required":["choice"],"not":{"required":["value"]},"properties":{"mode":{"const":"choice"}}}]}`)
	fields, err := deriveStateFields(repoRoot(t), state, schemaNode{}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []stateFieldModel{
		{Path: "/choice", ValueType: "string", Presence: "sometimes"},
		{Path: "/group/flag", ValueType: "boolean", Presence: "sometimes"},
		{Path: "/group/zero", ValueType: "number", Presence: "always", Quantity: &quantityModel{Unit: "1"}},
		{Path: "/mode", ValueType: "string", Presence: "always", Enum: []string{"value", "choice"}},
		{Path: "/value", ValueType: "number", Presence: "sometimes", Quantity: &quantityModel{Unit: "1"}},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
}

func TestStateFieldAnnotationsRejectInvalidShapesAndReferences(t *testing.T) {
	t.Parallel()
	support := `{"type":"object","required":["state"],"properties":{"state":{"type":"object","required":["maximum","unit"],"properties":{"maximum":{"type":"number","minimum":1},"unit":{"type":"string","minLength":1},"optional":{"type":"number","minimum":1},"zero":{"type":"number","minimum":0},"negative":{"type":"number","minimum":-1}}}}}`
	tests := []struct{ name, state, want string }{
		{"unknown key", `{"type":"number","hearth":{"units":"V"}}`, "invalid hearth"},
		{"null annotation", `{"type":"number","hearth":null}`, "invalid hearth"},
		{"empty unit", `{"type":"number","hearth":{"unit":""}}`, "invalid hearth"},
		{"wrong unit", `{"type":"number","hearth":{"unit":42}}`, "invalid hearth"},
		{
			"unknown reference key",
			`{"type":"number","hearth":{"unit":{"root":"support","path":"/state/unit","typo":true}}}`,
			"invalid hearth",
		},
		{"wrong root", `{"type":"number","hearth":{"unit":{"root":"state","path":"/unit"}}}`, "invalid hearth"},
		{"bad path", `{"type":"number","hearth":{"unit":{"root":"support","path":"/state/Unit"}}}`, "invalid hearth"},
		{
			"missing reference",
			`{"type":"number","hearth":{"unit":{"root":"support","path":"/state/missing"}}}`,
			"unresolved",
		},
		{
			"numeric unit",
			`{"type":"number","hearth":{"unit":{"root":"support","path":"/state/maximum"}}}`,
			"string support",
		},
		{"string annotation", `{"type":"string","hearth":{"unit":"1"}}`, "numeric scalar"},
		{"boolean annotation", `{"type":"boolean","hearth":{}}`, "numeric scalar"},
		{"container annotation", `{"type":"object","hearth":{}}`, "numeric scalar"},
		{"array annotation", `{"type":"array","items":{"type":"number","hearth":{"unit":"1"}}}`, "array paths"},
		{
			"optional scale",
			`{"type":"number","hearth":{"percent_of":{"root":"support","path":"/state/optional"}}}`,
			"required numeric",
		},
		{
			"zero scale",
			`{"type":"number","hearth":{"percent_of":{"root":"support","path":"/state/zero"}}}`,
			"positive lower bound",
		},
		{
			"negative scale",
			`{"type":"number","hearth":{"percent_of":{"root":"support","path":"/state/negative"}}}`,
			"positive lower bound",
		},
		{
			"string scale",
			`{"type":"number","hearth":{"percent_of":{"root":"support","path":"/state/unit"}}}`,
			"required numeric",
		},
		{
			"physical scale",
			`{"type":"number","hearth":{"unit":"V","percent_of":{"root":"support","path":"/state/maximum"}}}`,
			"fixed unit 1",
		},
		{
			"dynamic scale",
			`{"type":"number","hearth":{"unit":{"root":"support","path":"/state/unit"},"percent_of":{"root":"support","path":"/state/maximum"}}}`,
			"fixed unit 1",
		},
		{
			"conflicting branch",
			`{"type":"object","properties":{"value":{"type":"number"}},"oneOf":[{"properties":{"value":{"type":"integer","hearth":{"unit":"V"}}}}]}`,
			"ambiguous",
		},
		{"reference schema", `{"type":"number","$ref":"#/$defs/value"}`, "unsupported semantic"},
		{
			"hidden annotation",
			`{"type":"object","patternProperties":{".*":{"type":"number","hearth":{"unit":"V"}}}}`,
			"unsupported hearth",
		},
		{"unsupported allOf", `{"type":"number","allOf":[{"hearth":{"unit":"V"}}]}`, "unsupported semantic"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := deriveStateFields(repoRoot(t), fieldSchema(t, test.state), fieldSchema(t, support), false)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), `field ""`) {
				t.Fatalf("error = %v, want field context and %q", err, test.want)
			}
		})
	}
}

func TestStateFieldsDeriveRequiredAndOptionalInterpretation(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"numericsensor", "numericsetting", "brightness"} {
		model, err := loadModel(filepath.Join(repoRoot(t), "entitytypes", name+"v1", "entitytype.json"))
		if err != nil {
			t.Fatal(err)
		}
		var numeric *quantityModel
		for _, field := range model.StateFields {
			if field.Quantity != nil {
				numeric = field.Quantity
			}
		}
		if numeric == nil {
			t.Fatalf("%s missing numeric field", name)
		}
		if numeric.UnitOptional != (name == "numericsetting") {
			t.Fatalf("%s optional = %v", name, numeric.UnitOptional)
		}
		if (numeric.PercentOf != nil) != (name == "brightness") {
			t.Fatalf("%s percent_of = %v", name, numeric.PercentOf)
		}
	}
}

func TestInterpretationReferencesFollowRequiredAncestorsAndPositiveBounds(t *testing.T) {
	t.Parallel()
	state := fieldSchema(t, `{"type":"number","hearth":{"percent_of":{"root":"support","path":"/state/maximum"}}}`)
	for _, test := range []struct {
		name, support string
		valid         bool
	}{
		{"optional ancestor", `{"type":"object","properties":{"state":{"type":"object","required":["maximum"],"properties":{"maximum":{"type":"number","minimum":1}}}}}`, false},
		{"exclusive zero", `{"type":"object","required":["state"],"properties":{"state":{"type":"object","required":["maximum"],"properties":{"maximum":{"type":"number","exclusiveMinimum":0}}}}}`, true},
		{"exclusive negative", `{"type":"object","required":["state"],"properties":{"state":{"type":"object","required":["maximum"],"properties":{"maximum":{"type":"number","exclusiveMinimum":-1}}}}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := deriveStateFields(repoRoot(t), state, fieldSchema(t, test.support), false)
			if (err == nil) != test.valid {
				t.Fatalf("error=%v valid=%v", err, test.valid)
			}
		})
	}
}

func TestStateFieldsJSONIsOwnedAndChecked(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	models := []entityTypeModel{
		{
			Package: "pressurev1",
			TypeID:  "hearth.pressure/v1",
			StateFields: []stateFieldModel{
				{Path: "", ValueType: "number", Presence: "always", Quantity: &quantityModel{Unit: "hPa"}},
			},
		},
	}
	outputs, renderErr := stateFieldOutputs(root, models)
	if renderErr != nil {
		t.Fatal(renderErr)
	}
	generated := outputs[1]
	if !strings.Contains(string(generated.content), `"quantity": {`) ||
		!strings.Contains(string(generated.content), `"unit": "hPa"`) {
		t.Fatalf("JSON = %s", generated.content)
	}
	if err := applyOutputs(root, []output{generated}, true); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing = %v", err)
	}
	if err := applyOutputs(root, []output{generated}, false); err != nil {
		t.Fatal(err)
	}
	if err := applyOutputs(root, []output{generated}, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generated.path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyOutputs(root, []output{generated}, true); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale = %v", err)
	}
	if err := applyOutputs(root, nil, true); err == nil || !strings.Contains(err.Error(), "orphaned") {
		t.Fatalf("orphan = %v", err)
	}
}
