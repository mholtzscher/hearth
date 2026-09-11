package main

import (
	"bytes"
	"encoding/json"
	"go/format"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
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

func TestLoadModelRequiresManifestOperationsToMatchSupport(t *testing.T) {
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
	directory := filepath.Join(root, "entitytypes", "examplev1")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, "state.schema.json"), map[string]any{
		"$id": "urn:test:state", "type": "boolean",
	})
	writeJSON(t, filepath.Join(directory, "support.schema.json"), map[string]any{
		"$id": "urn:test:support", "type": "object", "additionalProperties": false,
		"required": []string{"state", "operations"},
		"properties": map[string]any{
			"state": map[string]any{"type": "object", "additionalProperties": false},
			"operations": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"set": map[string]any{"type": "object", "additionalProperties": false}},
			},
		},
	})
	writeJSON(t, filepath.Join(directory, "entitytype.json"), map[string]any{
		"manifest_version": 1,
		"type":             "example.value/v1",
		"state_schema":     "state.schema.json",
		"support_schema":   "support.schema.json",
		"operations":       map[string]any{},
		"examples":         "examples.json",
	})

	_, err := loadModel(filepath.Join(directory, "entitytype.json"))
	if err == nil || !strings.Contains(err.Error(), "exactly match") {
		t.Fatalf("manifest mismatch error = %v", err)
	}
}

func TestLoadModelEnforcesAuthoritativeManifestSchema(t *testing.T) {
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
	directory := filepath.Join(root, "entitytypes", "examplev1")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, "entitytype.json"), map[string]any{
		"manifest_version": 1,
		"type":             "example.value/v1",
		"state_schema":     "state.schema.json",
		"support_schema":   "support.schema.json",
		"examples":         "examples.json",
	})

	_, err := loadModel(filepath.Join(directory, "entitytype.json"))
	if err == nil || !strings.Contains(err.Error(), "validate manifest schema") ||
		!strings.Contains(err.Error(), "operations") {
		t.Fatalf("missing operations error = %v", err)
	}
}

func TestLoadModelRejectsIntegerSchemasOutsideInt64(t *testing.T) {
	t.Parallel()
	for name, bounds := range map[string]map[string]any{
		"unbounded":     {},
		"below minimum": {"minimum": json.Number("-9223372036854775809"), "maximum": 0},
		"above maximum": {"minimum": 0, "maximum": json.Number("9223372036854775808")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			directory, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
			schema := map[string]any{"$id": "urn:test:state", "type": "integer"}
			maps.Copy(schema, bounds)
			writeJSON(t, filepath.Join(directory, "state.schema.json"), schema)

			_, err := loadModel(manifestPath)
			if err == nil || !strings.Contains(err.Error(), "int64") {
				t.Fatalf("integer binding error = %v", err)
			}
		})
	}
}

func TestLoadModelNormalizesExamplesPathForEmbed(t *testing.T) {
	t.Parallel()
	_, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
	definition := minimalManifest("example.value/v1")
	definition["examples"] = "./examples.json"
	writeJSON(t, manifestPath, definition)

	model, err := loadModel(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if model.ExamplesFile != "examples.json" {
		t.Fatalf("ExamplesFile = %q, want cleaned slash-safe path", model.ExamplesFile)
	}
	conformance := renderConformanceTest(model, "example.test")
	if !strings.Contains(string(conformance.content), `//go:embed "examples.json"`) {
		t.Fatalf("generated conformance test does not embed the normalized path:\n%s", conformance.content)
	}
}

func TestLoadModelRejectsExamplesEmbedPatternMetacharacters(t *testing.T) {
	t.Parallel()
	for _, examplesPath := range []string{"*.json", "examples[0].json", "examples?.json", `examples\.json`} {
		t.Run(examplesPath, func(t *testing.T) {
			t.Parallel()
			_, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
			definition := minimalManifest("example.value/v1")
			definition["examples"] = examplesPath
			writeJSON(t, manifestPath, definition)

			_, err := loadModel(manifestPath)
			if err == nil || !strings.Contains(err.Error(), "unsupported Go embed pattern metacharacter") {
				t.Fatalf("examples path %q error = %v", examplesPath, err)
			}
		})
	}
}

func TestLoadModelRejectsExamplesEmbedAllPrefix(t *testing.T) {
	t.Parallel()
	_, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
	definition := minimalManifest("example.value/v1")
	definition["examples"] = "all:examples.json"
	writeJSON(t, manifestPath, definition)

	_, err := loadModel(manifestPath)
	if err == nil || !strings.Contains(err.Error(), "unsupported Go embed pattern prefix") {
		t.Fatalf("examples path all: prefix error = %v", err)
	}
}

func TestLoadModelRejectsExamplesEmbedInvalidFilename(t *testing.T) {
	t.Parallel()
	directory, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
	invalidPath := filepath.Join(directory, "examples:valid.json")
	if err := os.Rename(filepath.Join(directory, "examples.json"), invalidPath); err != nil {
		t.Fatal(err)
	}
	definition := minimalManifest("example.value/v1")
	definition["examples"] = "examples:valid.json"
	writeJSON(t, manifestPath, definition)

	_, err := loadModel(manifestPath)
	if err == nil || !strings.Contains(err.Error(), `component "examples:valid.json" is not valid for Go embed`) ||
		!strings.Contains(err.Error(), "invalid character ':'") {
		t.Fatalf("invalid examples filename error = %v", err)
	}
}

func TestLoadModelRejectsExamplesFileSymlink(t *testing.T) {
	t.Parallel()
	directory, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
	target := filepath.Join(directory, "examples-target.json")
	if err := os.Rename(filepath.Join(directory, "examples.json"), target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(target), filepath.Join(directory, "examples-link.json")); err != nil {
		t.Fatal(err)
	}

	assertExamplesPathRejected(t, manifestPath, "examples-link.json")
}

func TestLoadModelRejectsExamplesIntermediateDirectorySymlink(t *testing.T) {
	t.Parallel()
	directory, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
	target := filepath.Join(directory, "examples-directory")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(directory, "examples.json"), filepath.Join(target, "examples.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(target), filepath.Join(directory, "examples-link")); err != nil {
		t.Fatal(err)
	}

	assertExamplesPathRejected(t, manifestPath, "examples-link/examples.json")
}

func assertExamplesPathRejected(t *testing.T, manifestPath, examplesPath string) {
	t.Helper()
	definition := minimalManifest("example.value/v1")
	definition["examples"] = examplesPath
	writeJSON(t, manifestPath, definition)

	_, err := loadModel(manifestPath)
	if err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("symlink examples path error = %v", err)
	}
}

func TestLoadModelRejectsExamplesNestedModule(t *testing.T) {
	t.Parallel()
	directory, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
	nested := filepath.Join(directory, "examples-directory")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(directory, "examples.json"), filepath.Join(nested, "examples.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module example.test/nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	definition := minimalManifest("example.value/v1")
	definition["examples"] = "examples-directory/examples.json"
	writeJSON(t, manifestPath, definition)

	_, err := loadModel(manifestPath)
	if err == nil || !strings.Contains(err.Error(), `component "examples-directory" is a nested Go module`) {
		t.Fatalf("nested module examples path error = %v", err)
	}
}

func TestLoadModelRejectsLongTypeID(t *testing.T) {
	t.Parallel()
	directory, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
	writeJSON(t, filepath.Join(directory, "entitytype.json"), minimalManifest(strings.Repeat("a", 126)+"/v1"))

	if _, err := loadModel(manifestPath); err == nil {
		t.Fatal("long Entity type ID unexpectedly accepted")
	}
}

func TestLoadModelRejectsDuplicateSchemaIDs(t *testing.T) {
	t.Parallel()
	directory, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
	writeJSON(t, filepath.Join(directory, "support.schema.json"), minimalSupportSchema("urn:test:state"))

	_, err := loadModel(manifestPath)
	if err == nil || !strings.Contains(err.Error(), "duplicate schema ID") {
		t.Fatalf("duplicate schema ID error = %v", err)
	}
}

func TestRequireUniqueSchemaIDsIncludesOperationParameters(t *testing.T) {
	t.Parallel()
	err := requireUniqueSchemaIDs(
		schemaNode{ID: "urn:test:state"},
		schemaNode{ID: "urn:test:support"},
		[]operationModel{
			{Name: "first", ParametersSchema: schemaNode{ID: "urn:test:parameters"}},
			{Name: "second", ParametersSchema: schemaNode{ID: "urn:test:parameters"}},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate schema ID") {
		t.Fatalf("duplicate operation schema ID error = %v", err)
	}
}

func TestLoadModelRejectsExtraTopLevelSupportProperties(t *testing.T) {
	t.Parallel()
	directory, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
	support := minimalSupportSchema("urn:test:support")
	support["properties"].(map[string]any)["extra"] = map[string]any{"type": "boolean"}
	writeJSON(t, filepath.Join(directory, "support.schema.json"), support)

	_, err := loadModel(manifestPath)
	if err == nil || !strings.Contains(err.Error(), "unsupported top-level property") {
		t.Fatalf("extra support property error = %v", err)
	}
}

func TestLoadModelRejectsNonImportablePackageNames(t *testing.T) {
	t.Parallel()
	for _, packageName := range []string{"main", "internal", "_", "é"} {
		t.Run(packageName, func(t *testing.T) {
			t.Parallel()
			_, manifestPath := writeMinimalEntityTypeFixture(t, packageName)
			_, err := loadModel(manifestPath)
			if err == nil || !strings.Contains(err.Error(), "not an importable Go package name") {
				t.Fatalf("package name error = %v", err)
			}
		})
	}
}

func TestBehaviorRulesAreSchemaChecked(t *testing.T) {
	t.Parallel()
	integer := schemaNode{Type: "integer"}
	boolean := schemaNode{Type: "boolean"}
	support := schemaNode{
		Type: "object", Required: []string{"maximum", "enabled"},
		Properties: map[string]schemaNode{"maximum": integer, "enabled": boolean, "optional": integer},
	}
	roots := map[string]referenceRoot{
		"parameters": {Schema: schemaNode{
			Type: "object", Required: []string{"value"},
			Properties: map[string]schemaNode{"value": integer},
		}, GoExpression: "parameters"},
		"support": {Schema: support, GoExpression: "support"},
	}

	for operator, expected := range map[string]string{
		"gte": "int64(parameters.Value) >= int64(support.Maximum)",
		"lte": "int64(parameters.Value) <= int64(support.Maximum)",
	} {
		compiled, err := compileRule(ruleManifest{
			Op:    operator,
			Left:  referenceManifest{Root: "parameters", Path: "/value"},
			Right: &(referenceManifest{Root: "support", Path: "/maximum"}),
		}, roots)
		if err != nil {
			t.Fatal(err)
		}
		if condition := ruleCondition(compiled); condition != expected {
			t.Fatalf("%s condition = %s", operator, condition)
		}
	}

	for name, rule := range map[string]ruleManifest{
		"unknown root": {
			Op: "eq", Left: referenceManifest{Root: "state"}, Right: &(referenceManifest{Root: "parameters", Path: "/value"}),
		},
		"unknown path": {
			Op: "eq", Left: referenceManifest{Root: "parameters", Path: "/missing"}, Right: &(referenceManifest{Root: "parameters", Path: "/value"}),
		},
		"mismatched types": {
			Op: "eq", Left: referenceManifest{Root: "parameters", Path: "/value"}, Right: &(referenceManifest{Root: "support", Path: "/enabled"}),
		},
		"optional path": {
			Op: "eq", Left: referenceManifest{Root: "parameters", Path: "/value"}, Right: &(referenceManifest{Root: "support", Path: "/optional"}),
		},
		"non-numeric gte": {
			Op: "gte", Left: referenceManifest{Root: "support", Path: "/enabled"}, Right: &(referenceManifest{Root: "support", Path: "/enabled"}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, compileErr := compileRule(rule, roots); compileErr == nil {
				t.Fatal("invalid behavior rule unexpectedly accepted")
			}
		})
	}
}

func TestOutcomeEnvironmentExcludesSupport(t *testing.T) {
	t.Parallel()
	_, err := compileRule(ruleManifest{
		Op:    "eq",
		Left:  referenceManifest{Root: "parameters", Path: ""},
		Right: &(referenceManifest{Root: "support", Path: ""}),
	}, map[string]referenceRoot{
		"parameters": {Schema: schemaNode{Type: "integer"}, GoExpression: "parameters"},
		"state":      {Schema: schemaNode{Type: "integer"}, GoExpression: "state"},
	})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("outcome support error = %v", err)
	}
}

func TestMultipleOfGuardsZeroDivisor(t *testing.T) {
	t.Parallel()
	rule := ruleModel{
		Op:       "multiple_of",
		Left:     referenceModel{Kind: kindInteger, GoExpression: "parameters.Value"},
		Right:    referenceModel{Kind: kindInteger, GoExpression: "operationSupport.Step"},
		HasRight: true,
	}
	condition := ruleCondition(rule)
	if !strings.Contains(condition, "operationSupport.Step) != 0") {
		t.Fatalf("multiple_of condition = %s", condition)
	}
}

func TestTypeEmitterBindsNumberToFloat64(t *testing.T) {
	t.Parallel()
	for name, schema := range map[string]schemaNode{
		"root": {Type: "number"},
		"property": {
			Type:                 "object",
			AdditionalProperties: json.RawMessage("false"),
			Properties: map[string]schemaNode{
				"value": {Type: "number"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			emitter := &typeEmitter{declarations: make(map[string]string)}
			if err := emitter.define("Value", schema); err != nil {
				t.Fatal(err)
			}
			if declaration := emitter.declarations["Value"]; !strings.Contains(declaration, "float64") {
				t.Fatalf("number binding = %s, want float64", declaration)
			}
		})
	}
}

func TestTypeEmitterPreservesOptionalObjectPresence(t *testing.T) {
	t.Parallel()
	emitter := &typeEmitter{declarations: make(map[string]string)}
	schema := schemaNode{
		Type: "object",
		Properties: map[string]schemaNode{
			"set": {Type: "object", AdditionalProperties: json.RawMessage("false")},
		},
		AdditionalProperties: json.RawMessage("false"),
	}
	if err := emitter.define("Operations", schema); err != nil {
		t.Fatal(err)
	}
	if declaration := emitter.declarations["Operations"]; !strings.Contains(declaration, "Set *OperationsSet") ||
		!strings.Contains(declaration, `json:"set,omitempty"`) {
		t.Fatalf("optional operation field = %s", declaration)
	}
}

func TestOperationFreeFacadeOmitsCommandArtifacts(t *testing.T) {
	t.Parallel()
	source := renderFacade(
		entityTypeModel{Package: "examplev1"},
		"example.test",
	)
	text := string(source.content)
	for _, forbidden := range []string{
		`"encoding/json"`,
		"type Handlers struct",
		"NewCommandHandler",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("operation-free facade contains %q", forbidden)
		}
	}
	for _, required := range []string{
		"NewEntityDescriptor",
		"NewObservation",
		"ObservationInput",
		`"github.com/mholtzscher/hearth/sdk/adapter/typed"`,
		"NewTypedEntityDescriptor",
		"NewTypedEntityObservation",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("operation-free facade does not contain %q", required)
		}
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

func TestLoadModelRejectsOperationWithoutExamples(t *testing.T) {
	t.Parallel()
	directory, manifestPath := writeOptionalOperationFixture(t)
	writeJSON(t, filepath.Join(directory, "examples.json"), map[string]any{
		"cases": []any{map[string]any{
			"name":    "disabled",
			"support": map[string]any{"state": map[string]any{}, "operations": map[string]any{}},
			"states": []any{
				map[string]any{"value": 1, "valid": true},
				map[string]any{"value": "on", "valid": false},
			},
			"operations": map[string]any{},
		}},
	})

	_, err := loadModel(manifestPath)
	if err == nil || !strings.Contains(err.Error(), `operation "activate" has no examples in any case`) {
		t.Fatalf("missing operation coverage error = %v", err)
	}
}

func TestRenderedConformanceEmbedsExactExamplesPath(t *testing.T) {
	t.Parallel()
	source := renderConformanceTest(entityTypeModel{
		Package:      "examplev1",
		TypeID:       "example.value/v1",
		ExamplesFile: "examples.json",
		Operations: []operationModel{
			{Name: "activate", GoName: "Activate"},
		},
	}, "example.test")
	text := string(source.content)
	for _, required := range []string{
		`//go:embed "examples.json"`,
		`"example.test/internal/entitytypetest"`,
		`"activate":`,
		"ValidateActivateParameters",
		"ActivateSatisfied",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("generated conformance test does not contain %q", required)
		}
	}
}

func TestRenderedConformanceOmitsOperationsForFreeTypes(t *testing.T) {
	t.Parallel()
	source := renderConformanceTest(entityTypeModel{
		Package:      "examplev1",
		TypeID:       "example.value/v1",
		ExamplesFile: "examples.json",
	}, "example.test")
	text := string(source.content)
	if !strings.Contains(text, "Operations: nil") {
		t.Errorf("operation-free conformance test does not declare nil operations:\n%s", text)
	}
	if strings.Contains(text, `"errors"`) {
		t.Errorf("operation-free conformance test imports errors:\n%s", text)
	}
}

func writeOptionalOperationFixture(t *testing.T) (string, string) {
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
		"$id": "urn:test:state", "type": "integer", "minimum": 0, "maximum": 100,
	})
	writeJSON(t, filepath.Join(directory, "support.schema.json"), map[string]any{
		"$id": "urn:test:support", "type": "object", "additionalProperties": false,
		"required": []string{"state", "operations"},
		"properties": map[string]any{
			"state": map[string]any{"type": "object", "additionalProperties": false},
			"operations": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"activate": map[string]any{"type": "object", "additionalProperties": false},
				},
			},
		},
	})
	writeJSON(t, filepath.Join(directory, "activate-parameters.schema.json"), map[string]any{
		"$id": "urn:test:activate", "type": "object", "additionalProperties": false,
		"required": []string{"value"},
		"properties": map[string]any{
			"value": map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
		},
	})
	writeJSON(t, filepath.Join(directory, "entitytype.json"), map[string]any{
		"manifest_version": 1,
		"type":             "example.value/v1",
		"state_schema":     "state.schema.json",
		"support_schema":   "support.schema.json",
		"examples":         "examples.json",
		"operations": map[string]any{"activate": map[string]any{
			"parameters_schema": "activate-parameters.schema.json", "deadline_ms": 1000, "outcome": "observed",
			"satisfied_when": []any{map[string]any{
				"op":    "lte",
				"left":  map[string]any{"root": "parameters", "path": "/value"},
				"right": map[string]any{"root": "state", "path": ""},
			}},
		}},
	})
	return directory, filepath.Join(directory, "entitytype.json")
}

func TestRenderedCodecsEmbedExactManifestPaths(t *testing.T) {
	t.Parallel()
	source := renderCodecs(entityTypeModel{
		Package:       "examplev1",
		StateFile:     "schemas/state.json",
		StateSchema:   schemaNode{ID: "urn:test:state"},
		SupportFile:   "support.schema.json",
		SupportSchema: schemaNode{ID: "urn:test:support"},
		Operations: []operationModel{{
			GoName: "Set", ParametersFile: "schemas/set-parameters.json",
			ParametersSchema: schemaNode{ID: "urn:test:set-parameters"},
		}},
	})
	directive := `//go:embed "schemas/set-parameters.json" "schemas/state.json" "support.schema.json"`
	if !strings.Contains(string(source.content), directive) {
		t.Errorf("generated codecs do not contain %s", directive)
	}
}

// testGeneratedPath names a generated file inside a fresh temporary
// destination directory. Formatting resolves imports relative to the
// destination directory, so formatter tests use the same shape of path that
// generation does.
func testGeneratedPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

// generatedImportPaths reports the imports a generated file declares, in
// source order.
func generatedImportPaths(t *testing.T, source []byte) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "zz_generated.go", source, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, source)
	}
	paths := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		path, unquoteErr := strconv.Unquote(spec.Path.Value)
		if unquoteErr != nil {
			t.Fatalf("generated import %s is not a quoted path: %v", spec.Path.Value, unquoteErr)
		}
		paths = append(paths, path)
	}
	return paths
}

// TestFormatGeneratedOutputsRemovesUnusedImports pins the contract renderers
// rely on: a renderer may declare an import block without hand-tuning it,
// because the central formatting stage keeps exactly the imports the generated
// code uses. Generated packages therefore compile, and generate-check stays
// byte-stable.
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

func writeMinimalEntityTypeFixture(t *testing.T, packageName string) (string, string) {
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
	directory := filepath.Join(root, "entitytypes", packageName)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(directory, "state.schema.json"), map[string]any{
		"$id": "urn:test:state", "type": "boolean",
	})
	writeJSON(t, filepath.Join(directory, "support.schema.json"), minimalSupportSchema("urn:test:support"))
	writeJSON(t, filepath.Join(directory, "examples.json"), map[string]any{
		"cases": []any{map[string]any{
			"name": "minimal", "support": map[string]any{"state": map[string]any{}, "operations": map[string]any{}},
			"states": []any{
				map[string]any{"value": true, "valid": true},
				map[string]any{"value": 1, "valid": false},
			},
			"operations": map[string]any{},
		}},
	})
	writeJSON(t, filepath.Join(directory, "entitytype.json"), minimalManifest("example.value/v1"))
	return directory, filepath.Join(directory, "entitytype.json")
}

func minimalManifest(typeID string) map[string]any {
	return map[string]any{
		"manifest_version": 1,
		"type":             typeID,
		"state_schema":     "state.schema.json",
		"support_schema":   "support.schema.json",
		"operations":       map[string]any{},
		"examples":         "examples.json",
	}
}

func jsonNumber(value string) *json.Number {
	number := json.Number(value)
	return &number
}

func minimalSupportSchema(id string) map[string]any {
	return map[string]any{
		"$id": id, "type": "object", "additionalProperties": false,
		"required": []string{"state", "operations"},
		"properties": map[string]any{
			"state": map[string]any{"type": "object", "maxProperties": 0, "additionalProperties": false},
			"operations": map[string]any{
				"type": "object", "maxProperties": 0, "additionalProperties": false,
			},
		},
	}
}

func writeManifestSchema(t *testing.T, root string) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate generator test")
	}
	raw, readErr := os.ReadFile(
		filepath.Join(filepath.Dir(filename), "../../../entitytypes/entitytype-manifest.schema.json"),
	)
	if readErr != nil {
		t.Fatal(readErr)
	}
	path := filepath.Join(root, "entitytypes", "entitytype-manifest.schema.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogProbeSelectsSupportLevelRejections(t *testing.T) {
	t.Parallel()
	model := writeCatalogProbeFixture(t)
	probe, err := selectCatalogProbe(model, newCatalogSchemaChecker())
	if err != nil {
		t.Fatal(err)
	}
	if string(probe.validState) != `75` {
		t.Fatalf("valid State = %s, want 75", probe.validState)
	}
	// The schema-invalid "loud" rep is listed first; selection must skip it.
	if string(probe.supportInvalidState) != `85` {
		t.Fatalf("support-invalid State = %s, want 85", probe.supportInvalidState)
	}
	if len(probe.operations) != 1 {
		t.Fatalf("operations = %d, want 1", len(probe.operations))
	}
	operation := probe.operations[0]
	if string(operation.parameters) != `{"value":75}` {
		t.Fatalf("parameters = %s", operation.parameters)
	}
	if string(operation.supportInvalidParams) != `{"value":76}` {
		t.Fatalf("support-invalid parameters = %s", operation.supportInvalidParams)
	}
	if operation.model.DeadlineMS != 10000 {
		t.Fatalf("deadline = %d, want 10000", operation.model.DeadlineMS)
	}
	if string(operation.satisfiedState) != `75` || string(operation.unsatisfiedState) != `70` {
		t.Fatalf(
			"outcome states = %s, %s",
			operation.satisfiedState,
			operation.unsatisfiedState,
		)
	}
	if string(probe.unequalState) != `70` {
		t.Fatalf("unequal State = %s, want 70", probe.unequalState)
	}
}

func TestRenderedCatalogWiringCoversDeadlineOutcomesAndEquality(t *testing.T) {
	t.Parallel()
	model := writeCatalogProbeFixture(t)
	rendered, err := renderCatalogConformanceTest([]entityTypeModel{model}, "example.test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered.content)
	for _, required := range []string{
		"TestGeneratedBuiltinCatalogWiring",
		`"example.value/v1"`,
		"10000*time.Millisecond",
		"support-invalid State unexpectedly accepted",
		"support-invalid set parameters unexpectedly accepted",
		"catalog set satisfied outcome",
		"catalog set unsatisfied outcome",
		"catalog equal State",
		"catalog unequal State",
		"entitytypetest.EqualJSON",
		// A malformed persisted support must stay false, nil for a non-event
		// type rather than becoming a catalog failure.
		"malformedNonEventEntity",
		"catalog non-event type with malformed support accepted an Entity Event",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("rendered catalog wiring does not contain %q", required)
		}
	}
}

func TestCatalogProbeOmitsMissingSupportLevelRejections(t *testing.T) {
	t.Parallel()
	_, manifestPath := writeMinimalEntityTypeFixture(t, "examplev1")
	model, err := loadModel(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := selectCatalogProbe(model, newCatalogSchemaChecker())
	if err != nil {
		t.Fatal(err)
	}
	// The only invalid State (1 for a boolean schema) is schema-invalid, and
	// the single valid State leaves no unequal partner.
	if len(probe.supportInvalidState) != 0 {
		t.Fatalf("support-invalid State = %s, want none", probe.supportInvalidState)
	}
	if len(probe.unequalState) != 0 {
		t.Fatalf("unequal State = %s, want none", probe.unequalState)
	}
	rendered, err := renderCatalogConformanceTest([]entityTypeModel{model}, "example.test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered.content)
	for _, forbidden := range []string{"support-invalid", "unequal State"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("rendered catalog wiring unexpectedly contains %q", forbidden)
		}
	}
	if !strings.Contains(text, "catalog equal State") {
		t.Errorf("rendered catalog wiring omits the equal-State probe")
	}
}

func writeCatalogProbeFixture(t *testing.T) entityTypeModel {
	t.Helper()
	directory := t.TempDir()
	writeJSON(t, filepath.Join(directory, "state.schema.json"), map[string]any{
		"$id": "urn:test:catalog:state", "type": "integer", "minimum": 0, "maximum": 100,
	})
	writeJSON(t, filepath.Join(directory, "support.schema.json"), map[string]any{
		"$id": "urn:test:catalog:support", "type": "object", "additionalProperties": false,
		"required": []string{"state", "operations"},
		"properties": map[string]any{
			"state": map[string]any{
				"type": "object", "additionalProperties": false,
				"required":   []string{"maximum"},
				"properties": map[string]any{"maximum": map[string]any{"type": "integer"}},
			},
			"operations": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"set": map[string]any{"type": "object", "additionalProperties": false},
				},
			},
		},
	})
	writeJSON(t, filepath.Join(directory, "set-parameters.schema.json"), map[string]any{
		"$id": "urn:test:catalog:set-parameters", "type": "object", "additionalProperties": false,
		"required": []string{"value"},
		"properties": map[string]any{
			"value": map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
		},
	})
	raw := func(value string) json.RawMessage { return json.RawMessage(value) }
	return entityTypeModel{
		Package: "examplev1", Directory: directory, ModuleRoot: directory, TypeID: "example.value/v1",
		StateFile: "state.schema.json",
		Operations: []operationModel{
			{
				Name:           "set",
				GoName:         "Set",
				ParametersFile: "set-parameters.schema.json",
				DeadlineMS:     10000,
				Outcome:        outcomeObserved,
			},
		},
		Examples: examplesFile{Cases: []exampleCase{{
			Name:    "dim",
			Support: raw(`{"state":{"maximum":80},"operations":{"set":{"step":5}}}`),
			States: []validityExample{
				{Value: raw(`75`), Valid: true},
				{Value: raw(`"loud"`), Valid: false},
				{Value: raw(`85`), Valid: false},
			},
			Operations: map[string]operationExamples{"set": {
				Parameters: []validityExample{
					{Value: raw(`{"value":75}`), Valid: true},
					{Value: raw(`{"value":"loud"}`), Valid: false},
					{Value: raw(`{"value":76}`), Valid: false},
				},
				Outcomes: []outcomeExample{
					{Parameters: raw(`{"value":75}`), State: raw(`75`), Satisfied: true},
					{Parameters: raw(`{"value":75}`), State: raw(`70`), Satisfied: false},
				},
			}},
		}}},
	}
}

func TestLoadModelValidatesOperationOutcome(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		patch map[string]any
		want  string
	}{
		"unknown outcome rejected by manifest schema": {
			patch: map[string]any{"outcome": "dispatched-energy"},
			want:  "validate manifest schema",
		},
		"dispatched requires empty satisfied_when": {
			patch: map[string]any{"outcome": "dispatched"},
			want:  "must declare empty satisfied_when",
		},
		"observed requires satisfied_when": {
			patch: map[string]any{"outcome": "observed", "satisfied_when": []any{}},
			want:  "requires satisfied_when",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, manifestPath := writeOptionalOperationFixture(t)
			patchManifestOperation(t, manifestPath, test.patch)
			if _, err := loadModel(manifestPath); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("outcome error = %v, want %q", err, test.want)
			}
		})
	}
	t.Run("dispatched stateless operation loads", func(t *testing.T) {
		t.Parallel()
		_, manifestPath := writeDispatchedOperationFixture(t)
		patchManifestTopLevel(t, manifestPath, "stateless", true)
		model, err := loadModel(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		if !model.Stateless {
			t.Fatal("stateless was not parsed into the model")
		}
		if len(model.Operations) != 1 || model.Operations[0].Outcome != outcomeDispatched {
			t.Fatalf("operation outcome = %+v, want dispatched", model.Operations)
		}
		if len(model.Operations[0].SatisfiedWhen) != 0 {
			t.Fatalf("dispatched satisfied_when = %d rules, want none", len(model.Operations[0].SatisfiedWhen))
		}
	})
	t.Run("dispatched rejects authored outcomes", func(t *testing.T) {
		t.Parallel()
		directory, manifestPath := writeDispatchedOperationFixture(t)
		writeJSON(t, filepath.Join(directory, "examples.json"), map[string]any{
			"cases": []any{
				map[string]any{
					"name": "enabled",
					"support": map[string]any{
						"state":      map[string]any{},
						"operations": map[string]any{"activate": map[string]any{}},
					},
					"states": []any{
						map[string]any{"value": 1, "valid": true},
						map[string]any{"value": "on", "valid": false},
					},
					"operations": map[string]any{"activate": map[string]any{
						"parameters": []any{
							map[string]any{"value": map[string]any{"value": 1}, "valid": true},
							map[string]any{"value": map[string]any{"value": "on"}, "valid": false},
						},
						"outcomes": []any{
							map[string]any{
								"parameters": map[string]any{"value": 1},
								"state":      1,
								"satisfied":  true,
							},
						},
					}},
				},
			},
		})
		if _, err := loadModel(manifestPath); err == nil ||
			!strings.Contains(err.Error(), "must declare no outcomes") {
			t.Fatalf("dispatched outcome error = %v", err)
		}
	})
}

func writeDispatchedOperationFixture(t *testing.T) (string, string) {
	t.Helper()
	directory, manifestPath := writeOptionalOperationFixture(t)
	patchManifestOperation(t, manifestPath, map[string]any{
		"outcome":        "dispatched",
		"satisfied_when": []any{},
	})
	// Dispatched operations declare no outcome predicate, so the fixture
	// carries no satisfaction outcomes.
	writeJSON(t, filepath.Join(directory, "examples.json"), map[string]any{
		"cases": []any{
			map[string]any{
				"name": "enabled",
				"support": map[string]any{
					"state":      map[string]any{},
					"operations": map[string]any{"activate": map[string]any{}},
				},
				"states": []any{
					map[string]any{"value": 1, "valid": true},
					map[string]any{"value": "on", "valid": false},
				},
				"operations": map[string]any{"activate": map[string]any{
					"parameters": []any{
						map[string]any{"value": map[string]any{"value": 1}, "valid": true},
						map[string]any{"value": map[string]any{"value": "on"}, "valid": false},
					},
					"outcomes": []any{},
				}},
			},
		},
	})
	return directory, manifestPath
}

func patchManifestOperation(t *testing.T, manifestPath string, patch map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var definition map[string]any
	if unmarshalErr := json.Unmarshal(raw, &definition); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	operations, ok := definition["operations"].(map[string]any)
	if !ok {
		t.Fatal("manifest has no operations object")
	}
	activate, ok := operations["activate"].(map[string]any)
	if !ok {
		t.Fatal("manifest has no activate operation")
	}
	maps.Copy(activate, patch)
	writeJSON(t, manifestPath, definition)
}

func patchManifestTopLevel(t *testing.T, manifestPath, key string, value any) {
	t.Helper()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var definition map[string]any
	if unmarshalErr := json.Unmarshal(raw, &definition); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	definition[key] = value
	writeJSON(t, manifestPath, definition)
}

func TestCheckInvalidSupportsRequiresPairing(t *testing.T) {
	t.Parallel()
	rules := []ruleModel{{Op: "eq"}}
	t.Run("rules without examples", func(t *testing.T) {
		t.Parallel()
		if err := checkInvalidSupports(t.TempDir(), "support.schema.json", rules, examplesFile{}); err == nil ||
			!strings.Contains(err.Error(), "requires invalid_supports") {
			t.Fatalf("missing invalid supports error = %v", err)
		}
	})
	t.Run("examples without rules", func(t *testing.T) {
		t.Parallel()
		examples := examplesFile{InvalidSupports: []json.RawMessage{json.RawMessage(`{}`)}}
		if err := checkInvalidSupports(t.TempDir(), "support.schema.json", nil, examples); err == nil ||
			!strings.Contains(err.Error(), "requires support validation") {
			t.Fatalf("unexpected invalid supports error = %v", err)
		}
	})
	t.Run("neither", func(t *testing.T) {
		t.Parallel()
		if err := checkInvalidSupports(t.TempDir(), "support.schema.json", nil, examplesFile{}); err != nil {
			t.Fatalf("paired absence error = %v", err)
		}
	})
	t.Run("schema-decodable invalid support", func(t *testing.T) {
		t.Parallel()
		directory, _ := writeMinimalEntityTypeFixture(t, "examplev1")
		examples := examplesFile{InvalidSupports: []json.RawMessage{
			json.RawMessage(`{"state":{},"operations":{}}`),
		}}
		if err := checkInvalidSupports(directory, "support.schema.json", rules, examples); err != nil {
			t.Fatalf("decodable invalid support error = %v", err)
		}
	})
	t.Run("non-decodable invalid support", func(t *testing.T) {
		t.Parallel()
		directory, _ := writeMinimalEntityTypeFixture(t, "examplev1")
		examples := examplesFile{InvalidSupports: []json.RawMessage{
			json.RawMessage(`{"state":[],"operations":{}}`),
		}}
		if err := checkInvalidSupports(directory, "support.schema.json", rules, examples); err == nil ||
			!strings.Contains(err.Error(), "must schema-decode") {
			t.Fatalf("non-decodable invalid support error = %v", err)
		}
	})
}

func TestRenderBehaviorEmitsValidateSupport(t *testing.T) {
	t.Parallel()
	source := renderBehavior(entityTypeModel{
		Package:     "examplev1",
		StateSchema: schemaNode{Type: string(kindBoolean)},
		StateSupport: schemaNode{
			Type:                 schemaTypeObject,
			AdditionalProperties: json.RawMessage("false"),
		},
		SupportValidation: []ruleModel{{
			Op:       "eq",
			Left:     referenceModel{Root: "support", Path: "/flag", Kind: kindBoolean, GoExpression: "support.Flag"},
			Right:    referenceModel{Root: "support", Path: "/other", Kind: kindBoolean, GoExpression: "support.Other"},
			HasRight: true,
		}},
	})
	text := string(source.content)
	for _, required := range []string{
		`"errors"`,
		"func ValidateSupport(support Support) error",
		"bool(support.Flag) == bool(support.Other)",
		"support/flag must be equal support/other",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("rendered behavior does not contain %q:\n%s", required, text)
		}
	}
}

func TestRenderBehaviorOmitsSatisfiedForDispatched(t *testing.T) {
	t.Parallel()
	source := renderBehavior(entityTypeModel{
		Package:     "examplev1",
		StateSchema: schemaNode{Type: string(kindBoolean)},
		Operations: []operationModel{
			{Name: "trigger", GoName: "Trigger", DeadlineMS: 1000, Outcome: outcomeDispatched},
		},
	})
	text := string(source.content)
	if strings.Contains(text, "Satisfied") {
		t.Errorf("dispatched behavior unexpectedly defines a satisfaction matcher:\n%s", text)
	}
	if !strings.Contains(text, "func ValidateTriggerParameters") {
		t.Errorf("dispatched behavior omits parameter validation:\n%s", text)
	}
}

func TestRenderBehaviorKeepsSatisfiedForObserved(t *testing.T) {
	t.Parallel()
	source := renderBehavior(entityTypeModel{
		Package:     "examplev1",
		StateSchema: schemaNode{Type: string(kindBoolean)},
		Operations: []operationModel{
			{
				Name: "set", GoName: "Set", DeadlineMS: 1000, Outcome: outcomeObserved,
				SatisfiedWhen: []ruleModel{{
					Op: "eq",
					Left: referenceModel{
						Root: "parameters", Path: "/value",
						Kind: kindBoolean, GoExpression: "parameters.Value",
					},
					Right: referenceModel{
						Root: "state", Path: "",
						Kind: kindBoolean, GoExpression: "state",
					},
					HasRight: true,
				}},
			},
		},
	})
	if !strings.Contains(string(source.content), "func SetSatisfied(") {
		t.Errorf("observed behavior omits the satisfaction matcher:\n%s", source.content)
	}
}

func TestRenderedContractOmitsSatisfiesForDispatched(t *testing.T) {
	t.Parallel()
	source := renderConformanceTest(entityTypeModel{
		Package:      "examplev1",
		TypeID:       "example.value/v1",
		ExamplesFile: "examples.json",
		Operations: []operationModel{
			{Name: "trigger", GoName: "Trigger", Outcome: outcomeDispatched, Required: true},
		},
	}, "example.test")
	text := string(source.content)
	if !strings.Contains(text, "Dispatched: true") {
		t.Errorf("dispatched conformance probe omits the dispatched marker:\n%s", text)
	}
	for _, forbidden := range []string{"Satisfies:", "TriggerSatisfied"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("dispatched conformance probe contains %q:\n%s", forbidden, text)
		}
	}
}

func TestRenderedContractKeepsSatisfiesForObserved(t *testing.T) {
	t.Parallel()
	source := renderConformanceTest(entityTypeModel{
		Package:      "examplev1",
		TypeID:       "example.value/v1",
		ExamplesFile: "examples.json",
		Operations: []operationModel{
			{Name: "set", GoName: "Set", Outcome: outcomeObserved, Required: true},
		},
	}, "example.test")
	text := string(source.content)
	if !strings.Contains(text, "Satisfies:") || !strings.Contains(text, "SetSatisfied") {
		t.Errorf("observed conformance probe omits the outcome matcher:\n%s", text)
	}
	if strings.Contains(text, "Dispatched:") {
		t.Errorf("observed conformance probe unexpectedly marks dispatched:\n%s", text)
	}
}

func TestStatelessFacadeOmitsObservation(t *testing.T) {
	t.Parallel()
	source := renderFacade(entityTypeModel{
		Package:   "examplev1",
		Stateless: true,
		Operations: []operationModel{
			{Name: "trigger", GoName: "Trigger"},
		},
	}, "example.test")
	text := string(source.content)
	for _, forbidden := range []string{"type ObservationInput struct", "func NewObservation("} {
		if strings.Contains(text, forbidden) {
			t.Errorf("stateless facade contains %q:\n%s", forbidden, text)
		}
	}
	for _, required := range []string{"NewEntityDescriptor", "NewCommandHandler", "type Handlers struct"} {
		if !strings.Contains(text, required) {
			t.Errorf("stateless facade omits %q:\n%s", required, text)
		}
	}
}

func TestFacadeConstructorsValidateSupport(t *testing.T) {
	t.Parallel()
	source := renderFacade(entityTypeModel{
		Package:    "examplev1",
		Operations: []operationModel{{Name: "set", GoName: "Set", Required: true}},
	}, "example.test")
	text := string(source.content)
	if count := strings.Count(text, "ValidateSupport(support)"); count != 2 {
		t.Errorf("facade validates support %d times, want descriptor and command handler", count)
	}
	descriptor, handler, found := strings.Cut(text, "func NewCommandHandler(")
	if !found {
		t.Fatal("facade omits NewCommandHandler")
	}
	handlerSchema := strings.Index(handler, "codecs.Support.Encode(support)")
	handlerSemantic := strings.Index(handler, "ValidateSupport(support)")
	if handlerSchema < 0 || handlerSemantic < 0 {
		t.Error("command handler omits schema or semantic support validation")
	} else if handlerSchema > handlerSemantic {
		t.Error("command handler runs semantic support validation before schema validation")
	}
	descriptorSchema := strings.Index(descriptor, "NewTypedEntityDescriptor(")
	descriptorSemantic := strings.Index(descriptor, "ValidateSupport(support)")
	if descriptorSchema < 0 || descriptorSemantic < 0 {
		t.Error("descriptor omits schema or semantic support validation")
	} else if descriptorSchema > descriptorSemantic {
		t.Error("descriptor runs semantic support validation before schema validation")
	}

	free := renderFacade(
		entityTypeModel{Package: "examplev1"},
		"example.test",
	)
	freeText := string(free.content)
	if count := strings.Count(freeText, "ValidateSupport(support)"); count != 1 {
		t.Errorf("operation-free facade validates support %d times, want descriptor only", count)
	}
	if !strings.Contains(freeText, "&adapter.ValidationError{Err: fmt.Errorf(\"invalid Entity support:") {
		t.Error("operation-free descriptor does not wrap invalid support as a validation error")
	}
}

func TestStatefulFacadeKeepsObservation(t *testing.T) {
	t.Parallel()
	source := renderFacade(entityTypeModel{
		Package: "examplev1",
		Operations: []operationModel{
			{Name: "set", GoName: "Set"},
		},
	}, "example.test")
	text := string(source.content)
	for _, required := range []string{"type ObservationInput struct", "func NewObservation("} {
		if !strings.Contains(text, required) {
			t.Errorf("stateful facade omits %q:\n%s", required, text)
		}
	}
}

func TestStatelessFacadeTestOmitsObservation(t *testing.T) {
	t.Parallel()
	_, manifestPath := writeDispatchedOperationFixture(t)
	patchManifestTopLevel(t, manifestPath, "stateless", true)
	model, err := loadModel(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderFacadeConformanceTest(model, "example.test")
	text := string(rendered.content)
	for _, forbidden := range []string{"NewObservation(", "ObservationInput{", "TestGeneratedObservationConformance"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("stateless facade test contains %q", forbidden)
		}
	}
	if !strings.Contains(text, "TestGeneratedStatelessOmitsObservation") {
		t.Errorf("stateless facade test omits the omission marker")
	}
	if !strings.Contains(text, "TestGeneratedCommandConformance") {
		t.Errorf("stateless facade test omits command conformance")
	}
}
