package main

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"runtime"
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
	conformance, err := renderConformanceTest(model, "example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conformance), `//go:embed "examples.json"`) {
		t.Fatalf("generated conformance test does not embed the normalized path:\n%s", conformance)
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
	source, err := renderFacade(entityTypeModel{Package: "examplev1"}, "example.test")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
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
	conformance, err := renderCatalogConformanceTest([]entityTypeModel{model}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(conformance.content), `"time"`) {
		t.Errorf("operation-free catalog conformance imports time:\n%s", conformance.content)
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
	source, err := renderConformanceTest(entityTypeModel{
		Package:      "examplev1",
		TypeID:       "example.value/v1",
		ExamplesFile: "examples.json",
		Operations: []operationModel{
			{Name: "activate", GoName: "Activate"},
		},
	}, "example.test")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
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
	source, err := renderConformanceTest(entityTypeModel{
		Package:      "examplev1",
		TypeID:       "example.value/v1",
		ExamplesFile: "examples.json",
	}, "example.test")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
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
	source, err := renderCodecs(entityTypeModel{
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
	if err != nil {
		t.Fatal(err)
	}
	directive := `//go:embed "schemas/set-parameters.json" "schemas/state.json" "support.schema.json"`
	if !strings.Contains(string(source), directive) {
		t.Errorf("generated codecs do not contain %s", directive)
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
	rendered, err := renderCatalogConformanceTest([]entityTypeModel{model}, t.TempDir())
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
		"equalGeneratedCatalogJSON",
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
	rendered, err := renderCatalogConformanceTest([]entityTypeModel{model}, t.TempDir())
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
		_, manifestPath := writeDispatchedOperationFixture(t, true)
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
	t.Run("dispatched rejects unsatisfied outcomes", func(t *testing.T) {
		t.Parallel()
		_, manifestPath := writeDispatchedOperationFixture(t, false)
		if _, err := loadModel(manifestPath); err == nil ||
			!strings.Contains(err.Error(), "must expect satisfaction") {
			t.Fatalf("dispatched unsatisfied outcome error = %v", err)
		}
	})
}

func writeDispatchedOperationFixture(t *testing.T, satisfied bool) (string, string) {
	t.Helper()
	directory, manifestPath := writeOptionalOperationFixture(t)
	patchManifestOperation(t, manifestPath, map[string]any{
		"outcome":        "dispatched",
		"satisfied_when": []any{},
	})
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
						map[string]any{"parameters": map[string]any{"value": 1}, "state": 1, "satisfied": satisfied},
					},
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
	source, err := renderBehavior(entityTypeModel{
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
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
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
