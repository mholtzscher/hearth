package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testOperationCatalog struct {
	SchemaVersion int `json:"schema_version"`
	EntityTypes   map[string]struct {
		Operations map[string]struct {
			ParametersSchema json.RawMessage `json:"parameters_schema"`
		} `json:"operations"`
	} `json:"entity_types"`
}

func TestOperationParametersCatalogUsesLoadedManifestSchemas(t *testing.T) {
	t.Parallel()
	var models []entityTypeModel
	for _, name := range []string{"fixturemultiv1", "fixtureprecisionv1", "fixtureeventv1"} {
		model, err := loadModel(
			filepath.Join(
				repoRoot(t),
				"internal",
				"cmd",
				"entitytypegen",
				"testdata",
				"fixtures",
				name,
				"entitytype.json",
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		models = append(models, model)
	}
	generated, generateErr := operationParametersOutput(t.TempDir(), models)
	if generateErr != nil {
		t.Fatal(generateErr)
	}
	var document testOperationCatalog
	if err := json.Unmarshal(generated.content, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 1 || len(document.EntityTypes) != 3 {
		t.Fatalf("version = %d, types = %v", document.SchemaVersion, document.EntityTypes)
	}
	assertOperationCatalogSchemas(t, document, models)
	if !bytes.Contains(generated.content, []byte("9007199254740993e0")) {
		t.Fatalf("large schema number lost precision: %s", generated.content)
	}
	again, err := operationParametersOutput(
		filepath.Dir(filepath.Dir(generated.path)),
		[]entityTypeModel{models[2], models[1], models[0]},
	)
	if err != nil || !bytes.Equal(generated.content, again.content) {
		t.Fatalf("catalog is not deterministic under reordered models: %v", err)
	}
}

func assertOperationCatalogSchemas(t *testing.T, document testOperationCatalog, models []entityTypeModel) {
	t.Helper()
	for _, model := range models {
		entry, exists := document.EntityTypes[model.TypeID]
		if !exists || len(entry.Operations) != len(model.Operations) {
			t.Fatalf("operations for %s = %v", model.TypeID, entry.Operations)
		}
		for _, operation := range model.Operations {
			got, found := entry.Operations[operation.Name]
			if !found {
				t.Fatalf("missing %s/%s", model.TypeID, operation.Name)
			}
			var actual, source bytes.Buffer
			if err := json.Compact(&actual, got.ParametersSchema); err != nil {
				t.Fatal(err)
			}
			if err := json.Compact(&source, operation.ParametersSchema.Raw); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual.Bytes(), source.Bytes()) {
				t.Fatalf("schema for %s/%s = %s, want %s", model.TypeID, operation.Name, actual.Bytes(), source.Bytes())
			}
		}
	}
}

func TestOperationParametersCatalogIsOwnedAndChecked(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	generated, generateErr := operationParametersOutput(root, nil)
	if generateErr != nil {
		t.Fatal(generateErr)
	}
	if err := applyOutputs(root, []output{generated}, true); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing output error = %v", err)
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
		t.Fatalf("stale output error = %v", err)
	}
	if err := applyOutputs(root, nil, true); err == nil || !strings.Contains(err.Error(), "orphaned") {
		t.Fatalf("orphaned output error = %v", err)
	}
}

func TestOperationParametersSchemaLoadingErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, schema, want string }{
		{"missing", "", "no such file"},
		{"invalid JSON", `{`, "unexpected EOF"},
		{"not an object", `{"$id":"urn:test:parameters","type":"string"}`, "must describe an object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := copyOperationFixture(t)
			if tc.schema != "" {
				if err := os.WriteFile(
					filepath.Join(dir, "pulse-parameters.schema.json"),
					[]byte(tc.schema),
					0o644,
				); err != nil {
					t.Fatal(err)
				}
			}
			_, err := loadModel(filepath.Join(dir, "entitytype.json"))
			if err == nil || !strings.Contains(err.Error(), `operation "pulse" parameters schema`) ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("schema loading error = %v, want operation context and %q", err, tc.want)
			}
		})
	}
}

func copyOperationFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifestSchema(t, root)
	dir := filepath.Join(root, "entitytypes", "fixturemultiv1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(repoRoot(t), "internal", "cmd", "entitytypegen", "testdata", "fixtures", "fixturemultiv1")
	for _, name := range []string{"entitytype.json", "state.schema.json", "support.schema.json", "examples.json", "set-parameters.schema.json"} {
		raw, readErr := os.ReadFile(filepath.Join(source, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
