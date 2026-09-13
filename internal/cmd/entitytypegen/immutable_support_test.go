package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// immutableSupportFixtureSchema is the support schema the resolver tests walk:
// required scalar leaves at two depths plus one every rejection shape.
func immutableSupportFixtureSchema() schemaNode {
	return schemaNode{
		Type:                 schemaTypeObject,
		AdditionalProperties: json.RawMessage("false"),
		Required:             []string{"state", "operations"},
		Properties: map[string]schemaNode{
			"state": {
				Type:                 schemaTypeObject,
				AdditionalProperties: json.RawMessage("false"),
				Required:             []string{"kind", "bounds", "tags", "opaque"},
				Properties: map[string]schemaNode{
					"kind": {Type: string(kindString)},
					"bounds": {
						Type:                 schemaTypeObject,
						AdditionalProperties: json.RawMessage("false"),
						Required:             []string{"minimum"},
						Properties: map[string]schemaNode{
							"minimum": {Type: string(kindNumber)},
						},
					},
					"tags":   {Type: schemaTypeArray, Items: &schemaNode{Type: string(kindString)}},
					"opaque": {Type: "null"},
					"label":  {Type: string(kindString)},
					"wrapper": {
						Type:                 schemaTypeObject,
						AdditionalProperties: json.RawMessage("false"),
						Properties: map[string]schemaNode{
							"value": {Type: string(kindBoolean)},
						},
					},
				},
			},
			"operations": {
				Type:                 schemaTypeObject,
				AdditionalProperties: json.RawMessage("false"),
				MaxProperties:        new(int),
			},
		},
	}
}

func TestResolveImmutableSupportPathsAcceptsRequiredScalarLeaves(t *testing.T) {
	t.Parallel()
	paths, err := resolveImmutableSupportPaths(
		immutableSupportFixtureSchema(),
		[]string{"/state/kind", "/state/bounds/minimum"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %#v, want two", paths)
	}
	if paths[0].Field != "State.Kind" || paths[1].Field != "State.Bounds.Minimum" {
		t.Fatalf("resolved fields = %#v", paths)
	}
}

func TestResolveImmutableSupportPathsAcceptsEscapedTokens(t *testing.T) {
	t.Parallel()
	support := immutableSupportFixtureSchema()
	state := support.Properties["state"]
	state.Required = append(state.Required, "odd~name")
	state.Properties["odd~name"] = schemaNode{Type: string(kindBoolean)}
	support.Properties["state"] = state

	paths, err := resolveImmutableSupportPaths(support, []string{"/state/odd~0name"})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0].Field != "State.OddName" {
		t.Fatalf("resolved escaped path = %#v", paths)
	}
}

func TestResolveImmutableSupportPathsRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		pointer string
		want    string
	}{
		"empty":                 {pointer: "", want: "JSON Pointer"},
		"missing leading slash": {pointer: "state/kind", want: "JSON Pointer"},
		"empty reference token": {pointer: "/state//kind", want: "empty"},
		"trailing separator":    {pointer: "/state/kind/", want: "empty"},
		"invalid escape":        {pointer: "/state/~2kind", want: "escape"},
		"unterminated escape":   {pointer: "/state/kind~", want: "escape"},
		"missing path":          {pointer: "/state/missing", want: "does not exist"},
		"optional leaf":         {pointer: "/state/label", want: "optional"},
		"optional traversal":    {pointer: "/state/wrapper/value", want: "optional"},
		"object target":         {pointer: "/state/bounds", want: "scalar leaf"},
		"array target":          {pointer: "/state/tags", want: "scalar leaf"},
		"unsupported scalar":    {pointer: "/state/opaque", want: "unsupported scalar binding"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveImmutableSupportPaths(immutableSupportFixtureSchema(), []string{test.pointer})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolveImmutableSupportPathsRejectsDuplicates(t *testing.T) {
	t.Parallel()
	_, err := resolveImmutableSupportPaths(
		immutableSupportFixtureSchema(),
		[]string{"/state/kind", "/state/kind"},
	)
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestRenderBehaviorEmitsTypedSupportIdentityComparison(t *testing.T) {
	t.Parallel()
	source := renderBehavior(entityTypeModel{
		Package:     "examplev1",
		StateSchema: schemaNode{Type: string(kindBoolean)},
		ImmutableSupportPaths: []immutableSupportPath{
			{Pointer: "/state/kind", Field: "State.Kind"},
			{Pointer: "/state/bounds/minimum", Field: "State.Bounds.Minimum"},
		},
	})
	text := string(source.content)
	for _, required := range []string{
		"func SameSupportIdentity(previous, next Support) bool {",
		"previous.State.Kind == next.State.Kind",
		"previous.State.Bounds.Minimum == next.State.Bounds.Minimum",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("rendered behavior omits %q:\n%s", required, text)
		}
	}
}

func TestRenderBehaviorSupportIdentityDefaultsToTrue(t *testing.T) {
	t.Parallel()
	source := renderBehavior(entityTypeModel{
		Package:     "examplev1",
		StateSchema: schemaNode{Type: string(kindBoolean)},
	})
	if !strings.Contains(
		string(source.content),
		"func SameSupportIdentity(previous, next Support) bool {\n\treturn true\n}",
	) {
		t.Errorf("undeclared identity does not default to true:\n%s", source.content)
	}
}

func TestRenderCatalogWiresSupportIdentity(t *testing.T) {
	t.Parallel()
	catalog := renderCatalog(
		[]entityTypeModel{{Package: "examplev1", TypeID: "example.value/v1"}},
		"example.test",
		t.TempDir(),
	)
	if !strings.Contains(string(catalog.content), "contractexamplev1.SameSupportIdentity") {
		t.Fatalf("catalog does not pass the generated identity callback:\n%s", catalog.content)
	}
}

func TestLoadModelResolvesImmutableSupportPaths(t *testing.T) {
	t.Parallel()
	manifestPath := writeImmutablePathFixture(t, []string{"/state/kind"})
	model, err := loadModel(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.ImmutableSupportPaths) != 1 || model.ImmutableSupportPaths[0].Field != "State.Kind" {
		t.Fatalf("immutable paths = %#v", model.ImmutableSupportPaths)
	}
}

func TestLoadModelRejectsMalformedImmutableSupportPath(t *testing.T) {
	t.Parallel()
	manifestPath := writeImmutablePathFixture(t, []string{"/state"})
	_, err := loadModel(manifestPath)
	if err == nil || !strings.Contains(err.Error(), "immutable_support_paths") {
		t.Fatalf("malformed immutable path error = %v", err)
	}
}

func writeImmutablePathFixture(t *testing.T, pointers []string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "go.mod"),
		[]byte("module example.test\n\ngo 1.26\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	writeManifestSchema(t, root)
	directory := filepath.Join(root, "entitytypes", "examplev1")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, "state.schema.json"), map[string]any{
		"$id": "urn:test:state", "type": "number",
	})
	writeJSON(t, filepath.Join(directory, "support.schema.json"), map[string]any{
		"$id": "urn:test:support", "type": "object", "additionalProperties": false,
		"required": []string{"state", "operations"},
		"properties": map[string]any{
			"state": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"kind"},
				"properties": map[string]any{"kind": map[string]any{"type": "string"}},
			},
			"operations": map[string]any{
				"type": "object", "maxProperties": 0, "additionalProperties": false,
			},
		},
	})
	writeJSON(t, filepath.Join(directory, "examples.json"), map[string]any{
		"cases": []any{map[string]any{
			"name":    "identity",
			"support": map[string]any{"state": map[string]any{"kind": "a"}, "operations": map[string]any{}},
			"states": []any{
				map[string]any{"value": 1, "valid": true},
				map[string]any{"value": "a", "valid": false},
			},
			"operations": map[string]any{},
		}},
	})
	manifest := minimalManifest("example.value/v1")
	manifest["immutable_support_paths"] = pointers
	manifestPath := filepath.Join(directory, "entitytype.json")
	writeJSON(t, manifestPath, manifest)
	return manifestPath
}
