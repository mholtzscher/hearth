//go:build integration

package main

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestGeneratedFilesAreCurrent(t *testing.T) {
	t.Parallel()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate generator test")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../.."))
	if err := generateRoot(root, true); err != nil {
		t.Fatal(err)
	}
}

func TestOperationFreeCatalogConformanceOmitsTimeImport(t *testing.T) {
	t.Parallel()
	directory, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
	model, err := loadModel(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Operations) != 0 {
		t.Fatalf("fixture operations = %d, want 0", len(model.Operations))
	}
	conformance, err := renderCatalogConformanceTest([]entityTypeModel{model}, "example.test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := formatGeneratedOutputs([]output{conformance})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(formatted[0].content), `"time"`) {
		t.Errorf("operation-free catalog conformance imports time:\n%s", formatted[0].content)
	}
	_ = directory
}

// TestFormatGeneratedOutputsRemovesUnusedImports pins the contract renderers
// rely on: a renderer may declare an import block without hand-tuning it,
// because the central formatting stage keeps exactly the imports the generated
// code uses. Generated packages therefore compile with stable formatting.
func TestFormatGeneratedOutputsRemovesUnusedImports(t *testing.T) {
	t.Parallel()
	generated := output{path: testGeneratedPath(t, "zz_generated_example.go"), content: []byte(`package example

import (
	"errors"
	"fmt"
	"strings"
)

func describe(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("empty value")
	}
	return nil
}
`)}
	formatted, err := formatGeneratedOutputs([]output{generated})
	if err != nil {
		t.Fatal(err)
	}
	if got := generatedImportPaths(t, formatted[0].content); !slices.Equal(got, []string{"fmt", "strings"}) {
		t.Errorf("generated imports = %v, want the used imports only:\n%s", got, formatted[0].content)
	}
	unformatted, formatErr := format.Source(formatted[0].content)
	if formatErr != nil {
		t.Fatalf("formatted source is not valid Go: %v\n%s", formatErr, formatted[0].content)
	}
	if !bytes.Equal(unformatted, formatted[0].content) {
		t.Errorf("formatted source is not gofmt-clean:\n%s", formatted[0].content)
	}
	// Generation formats each file once and -check compares the result byte for
	// byte, so formatting a generated file twice must not change it.
	again, err := formatGeneratedOutputs(formatted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again[0].content, formatted[0].content) {
		t.Errorf("formatting is not idempotent:\n%s", again[0].content)
	}
}

// TestFormatGeneratedOutputsFormatsEachOutputAtItsOwnPath pins the central
// stage's key invariant: several outputs are formatted in one pass, and each
// keeps its own destination while its own unused imports are dropped.
func TestFormatGeneratedOutputsFormatsEachOutputAtItsOwnPath(t *testing.T) {
	t.Parallel()
	first := output{
		path: testGeneratedPath(t, "zz_generated_first.go"),
		content: []byte(`package first

import (
	"errors"
	"strings"
)

func describe(value string) string { return strings.TrimSpace(value) }
`),
	}
	second := output{
		path: testGeneratedPath(t, "zz_generated_second.go"),
		content: []byte(`package second

import (
	"errors"
	"fmt"
)

func describe() error { return fmt.Errorf("empty value") }
`),
	}
	formatted, err := formatGeneratedOutputs([]output{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if formatted[0].path != first.path || formatted[1].path != second.path {
		t.Errorf(
			"formatted paths = %q, %q, want %q, %q",
			formatted[0].path,
			formatted[1].path,
			first.path,
			second.path,
		)
	}
	if got := generatedImportPaths(t, formatted[0].content); !slices.Equal(got, []string{"strings"}) {
		t.Errorf("first output imports = %v, want only its used import:\n%s", got, formatted[0].content)
	}
	if got := generatedImportPaths(t, formatted[1].content); !slices.Equal(got, []string{"fmt"}) {
		t.Errorf("second output imports = %v, want only its used import:\n%s", got, formatted[1].content)
	}
}

// TestFormatGeneratedOutputsErrorNamesTheFailingOutputPath keeps a broken
// renderer diagnosable: when one of several outputs cannot be formatted, the
// error names that output's own destination and carries its unparsable source.
// Naming the second output also pins that every output is formatted against its
// own path rather than a shared one.
func TestFormatGeneratedOutputsErrorNamesTheFailingOutputPath(t *testing.T) {
	t.Parallel()
	valid := output{path: testGeneratedPath(t, "zz_generated_valid.go"), content: []byte("package example\n")}
	brokenPath := testGeneratedPath(t, "zz_generated_broken.go")
	broken := output{path: brokenPath, content: []byte("package broken\n\nfunc (\n")}
	_, err := formatGeneratedOutputs([]output{valid, broken})
	if err == nil {
		t.Fatal("unparsable generated source was accepted")
	}
	if !strings.Contains(err.Error(), brokenPath) {
		t.Errorf("error = %v, want it to name %s", err, brokenPath)
	}
	if strings.Contains(err.Error(), valid.path) {
		t.Errorf("error = %v, must not name the successfully formatted %s", err, valid.path)
	}
	if !strings.Contains(err.Error(), "func (") {
		t.Errorf("error = %v, want it to include the unparsable source", err)
	}
}

// TestFormatGeneratedOutputsDoesNotMutateInputs pins that the central stage
// returns fresh outputs: callers still hold the unformatted content they passed
// in, so they can compare pre- and post-formatting bytes.
func TestFormatGeneratedOutputsDoesNotMutateInputs(t *testing.T) {
	t.Parallel()
	unformatted := `package example

import (
	"errors"
	"fmt"
)

func describe() error { return fmt.Errorf("empty value") }
`
	generated := []output{{path: testGeneratedPath(t, "zz_generated_example.go"), content: []byte(unformatted)}}
	formatted, err := formatGeneratedOutputs(generated)
	if err != nil {
		t.Fatal(err)
	}
	if string(generated[0].content) != unformatted {
		t.Errorf("formatting mutated the caller's output:\n%s", generated[0].content)
	}
	if bytes.Equal(formatted[0].content, generated[0].content) {
		t.Error("formatting left the unused-import content unchanged")
	}
}

func TestRootGenerationAddsATypeWithoutPerTypeGo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "go.mod"),
		[]byte("module example.test\n\ngo 1.26\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	writeManifestSchema(t, root)
	directory := filepath.Join(root, "entitytypes", "switchv1")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(directory, "schemas"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, "schemas", "state.json"), map[string]any{
		"$id": "urn:test:switch:state", "type": "integer", "minimum": 0, "maximum": 100,
	})
	writeJSON(t, filepath.Join(directory, "parameters.schema.json"), map[string]any{
		"$id": "urn:test:switch:parameters", "type": "object", "additionalProperties": false,
		"required": []string{"value"}, "properties": map[string]any{
			"value": map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
		},
	})
	writeJSON(t, filepath.Join(directory, "support.schema.json"), map[string]any{
		"$id": "urn:test:switch:support", "type": "object", "additionalProperties": false,
		"required": []string{"state", "operations"},
		"properties": map[string]any{
			"state": map[string]any{"type": "object", "additionalProperties": false},
			"operations": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"set"},
				"properties": map[string]any{"set": map[string]any{"type": "object", "additionalProperties": false}},
			},
		},
	})
	writeJSON(t, filepath.Join(directory, "examples.json"), map[string]any{
		"cases": []any{
			map[string]any{
				"name": "switch",
				"support": map[string]any{
					"state":      map[string]any{},
					"operations": map[string]any{"set": map[string]any{}},
				},
				"states": []any{
					map[string]any{"value": 1, "valid": true},
					map[string]any{"value": "on", "valid": false},
				},
				"operations": map[string]any{"set": map[string]any{
					"parameters": []any{
						map[string]any{"value": map[string]any{"value": 1}, "valid": true},
						map[string]any{"value": map[string]any{"value": "on"}, "valid": false},
					},
					"outcomes": []any{
						map[string]any{"parameters": map[string]any{"value": 1}, "state": 2, "satisfied": true},
						map[string]any{"parameters": map[string]any{"value": 2}, "state": 1, "satisfied": false},
					},
				}},
			},
		},
	})
	writeJSON(t, filepath.Join(directory, "entitytype.json"), map[string]any{
		"manifest_version": 1,
		"type":             "example.switch/v1",
		"state_schema":     "schemas/state.json",
		"support_schema":   "support.schema.json",
		"examples":         "examples.json",
		"operations": map[string]any{"set": map[string]any{
			"parameters_schema": "parameters.schema.json", "deadline_ms": 1000, "outcome": "observed",
			"satisfied_when": []any{map[string]any{
				"op":    "lte",
				"left":  map[string]any{"root": "parameters", "path": "/value"},
				"right": map[string]any{"root": "state", "path": ""},
			}},
		}},
	})

	if err := generateRoot(root, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(directory, "zz_generated_types.go"),
		filepath.Join(directory, "zz_generated_behavior.go"),
		filepath.Join(directory, "zz_generated_conformance_test.go"),
		filepath.Join(root, "sdk", "adapter", "switchv1", "zz_generated_facade.go"),
		filepath.Join(root, "sdk", "adapter", "switchv1", "zz_generated_facade_test.go"),
		filepath.Join(root, "internal", "modules", "devices", "zz_generated_entitytypes.go"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("generated output %s: %v", path, err)
		}
	}
	codecs, err := os.ReadFile(filepath.Join(directory, "zz_generated_codecs.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codecs), `"schemas/state.json"`) {
		t.Fatalf("generated codecs do not embed the nested State schema:\n%s", codecs)
	}
	catalog, err := os.ReadFile(filepath.Join(root, "internal", "modules", "devices", "zz_generated_entitytypes.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(catalog), "newSwitchV1TypeDefinition") {
		t.Fatalf("generated catalog does not contain switch/v1:\n%s", catalog)
	}
	if checkErr := generateRoot(root, true); checkErr != nil {
		t.Fatal(checkErr)
	}
	orphan := filepath.Join(directory, "zz_generated_old.go")
	if writeErr := os.WriteFile(
		orphan,
		[]byte("// Code generated by entitytypegen; DO NOT EDIT.\n\npackage switchv1\n"),
		0o644,
	); writeErr != nil {
		t.Fatal(writeErr)
	}
	if checkErr := generateRoot(root, true); checkErr == nil || !strings.Contains(checkErr.Error(), "orphaned") {
		t.Fatalf("orphan check error = %v", checkErr)
	}
	if removeErr := generateRoot(root, false); removeErr != nil {
		t.Fatal(removeErr)
	}
	if _, statErr := os.Stat(orphan); !os.IsNotExist(statErr) {
		t.Fatalf("orphan still exists: %v", statErr)
	}
}
