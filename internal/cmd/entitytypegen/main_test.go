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

func TestTypeEmitterRejectsLossyNumberBindings(t *testing.T) {
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
			if err := emitter.define(
				"Value",
				schema,
			); err == nil ||
				!strings.Contains(err.Error(), "lossless binding") {
				t.Fatalf("number binding error = %v", err)
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
		`"github.com/mholtzscher/hearth/sdk/adapter/typed"`,
		"type Handlers struct",
		"NewCommandHandler",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("operation-free facade contains %q", forbidden)
		}
	}
	for _, required := range []string{"NewEntityDescriptor", "NewObservation", "ObservationInput"} {
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
			"parameters_schema": "activate-parameters.schema.json", "deadline_ms": 1000,
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
			"parameters_schema": "parameters.schema.json", "deadline_ms": 1000,
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
