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
		{"unknown kind", `{"type":"number","hearth":{"unit":"%","kind":"unknown"}}`, "invalid quantity kind"},
		{"incompatible kind", `{"type":"number","hearth":{"unit":"V","kind":"brightness"}}`, "invalid quantity kind"},
		{"kind without unit", `{"type":"number","hearth":{"kind":"brightness"}}`, "invalid quantity kind"},
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
			"removed percent_of is an unknown key",
			`{"type":"number","hearth":{"percent_of":{"root":"support","path":"/state/maximum"}}}`,
			"invalid hearth",
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

//nolint:gocognit // The table compares fixed and optional schema interpretations.
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
		if name == "brightness" && numeric.Unit != "%" {
			t.Fatalf("brightness unit = %v", numeric.Unit)
		}
		if name == "brightness" && numeric.Kind != "brightness" {
			t.Fatalf("brightness kind = %v", numeric.Kind)
		}
		if name != "brightness" {
			kind, ok := numeric.Kind.(referenceManifest)
			if !ok || kind.Path != "/state/quantity_kind" || !numeric.KindOptional {
				t.Fatalf("%s kind reference = %+v, optional %v", name, numeric.Kind, numeric.KindOptional)
			}
			behavior := string(renderBehavior(model).content)
			if !strings.Contains(
				behavior,
				`quantitykind.ValidateSupport(support, "/state/quantity_kind", "/state/unit", "")`,
			) {
				t.Fatalf("%s missing generated kind validation: %s", name, behavior)
			}
		}
	}
}

func TestInterpretationReferencesFollowRequiredAncestors(t *testing.T) {
	t.Parallel()
	state := fieldSchema(t, `{"type":"number","hearth":{"unit":{"root":"support","path":"/state/unit"}}}`)
	for _, test := range []struct {
		name, support string
		optional      bool
	}{
		{"optional ancestor", `{"type":"object","properties":{"state":{"type":"object","required":["unit"],"properties":{"unit":{"type":"string","minLength":1}}}}}`, true},
		{"required ancestors", `{"type":"object","required":["state"],"properties":{"state":{"type":"object","required":["unit"],"properties":{"unit":{"type":"string","minLength":1}}}}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fields, err := deriveStateFields(repoRoot(t), state, fieldSchema(t, test.support), false)
			if err != nil || len(fields) != 1 || fields[0].Quantity.UnitOptional != test.optional {
				t.Fatalf("fields=%+v error=%v optional=%v", fields, err, test.optional)
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

func TestRangeAnnotationsRequireNumericRequiredBounds(t *testing.T) {
	t.Parallel()
	support := fieldSchema(
		t,
		`{"type":"object","required":["minimum","maximum","text"],"properties":{"minimum":{"type":"number"},"maximum":{"type":"integer"},"optional":{"type":"number"},"text":{"type":"string"}}}`,
	)
	for _, tc := range []struct {
		name, rangeJSON string
		valid           bool
	}{
		{"literal", `{"minimum":0,"maximum":100}`, true},
		{"support", `{"minimum":{"root":"support","path":"/minimum"},"maximum":{"root":"support","path":"/maximum"}}`, true},
		{"equal", `{"minimum":100,"maximum":100}`, false},
		{"reversed", `{"minimum":100,"maximum":0}`, false},
		{"optional", `{"minimum":0,"maximum":{"root":"support","path":"/optional"}}`, false},
		{"text", `{"minimum":0,"maximum":{"root":"support","path":"/text"}}`, false},
		{"missing", `{"minimum":0}`, false},
		{"extra", `{"minimum":0,"maximum":100,"guess":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := fieldSchema(t, `{"type":"number","hearth":{"range":`+tc.rangeJSON+`}}`)
			fields, err := deriveStateFields(repoRoot(t), state, support, false)
			if (err == nil) != tc.valid {
				t.Fatalf("derive = %v", err)
			}
			if tc.valid && fields[0].Quantity.Range == nil {
				t.Fatal("range missing")
			}
		})
	}
}
