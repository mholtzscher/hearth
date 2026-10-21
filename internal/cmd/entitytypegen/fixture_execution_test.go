package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type fixtureExpectation struct {
	typeID     string
	operations int
	required   []string
	optional   []string
}

func fixtureExpectations() map[string]fixtureExpectation {
	return map[string]fixtureExpectation{
		"fixturefreev1":      {typeID: "fixture.free/v1"},
		"fixtureconstv1":     {typeID: "fixture.const/v1"},
		"fixturereqv1":       {typeID: "fixture.required/v1", operations: 1, required: []string{"set"}},
		"fixtureoptv1":       {typeID: "fixture.optional/v1", operations: 1, optional: []string{"activate"}},
		"fixtureprecisionv1": {typeID: "fixture.precision/v1", operations: 1, required: []string{"set"}},
		"fixturemultiv1": {
			typeID:     "fixture.multi/v1",
			operations: 2,
			required:   []string{"set"},
			optional:   []string{"pulse"},
		},
		"fixtureunknownv1": {
			typeID:     "fixture.unknown/v1",
			operations: 2,
			required:   []string{"unknown", "unknown-operation"},
		},
		"fixtureeventv1": {typeID: "fixture.event/v1"},
	}
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate generator test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "testdata", "fixtures"))
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate generator test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../.."))
}

// TestFixtureModelsLoad keeps fixture authoring failures cheap: every fixture
// must load with its expected operation matrix without launching a subprocess.
func TestFixtureModelsLoad(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)
	for name, expected := range fixtureExpectations() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkFixtureModel(t, root, name, expected)
		})
	}
}

func checkFixtureModel(t *testing.T, root, name string, expected fixtureExpectation) {
	t.Helper()
	model, err := loadModel(filepath.Join(root, name, "entitytype.json"))
	if err != nil {
		t.Fatal(err)
	}
	if model.TypeID != expected.typeID {
		t.Errorf("type ID = %q, want %q", model.TypeID, expected.typeID)
	}
	if len(model.Operations) != expected.operations {
		t.Fatalf("operations = %d, want %d", len(model.Operations), expected.operations)
	}
	checkOperationMode(t, model, expected.required, true)
	checkOperationMode(t, model, expected.optional, false)
}

func checkOperationMode(t *testing.T, model entityTypeModel, names []string, required bool) {
	t.Helper()
	mode := "optional"
	if required {
		mode = "required"
	}
	for _, name := range names {
		operation := findOperation(model, name)
		if operation == nil {
			t.Errorf("%s operation %q is missing", mode, name)
			continue
		}
		if operation.Required != required {
			t.Errorf("operation %q required = %v, want %v", name, operation.Required, required)
		}
	}
}

func findOperation(model entityTypeModel, name string) *operationModel {
	for index := range model.Operations {
		if model.Operations[index].Name == name {
			return &model.Operations[index]
		}
	}
	return nil
}

// TestFixtureCatalogProbes pins the regression shapes the bounded fixture
// execution covers: the optional fixture leads with the disabled case, so
// the operation probe must carry the originating enabled support rather
// than the first-case support; the required fixture spells numbers
// alternately (support maximum 8e1, valid State 75.0 with an equivalent
// sibling 7.5e1) and its only unequal State is the schema-valid but
// support-narrowed recorded outcome (85), which the generated catalog keeps
// as persisted with the valid State incoming. Selection structure is
// asserted here; TestGeneratedCodeConformance executes the generated catalog wiring
// built from these probes.
func TestFixtureCatalogProbes(t *testing.T) {
	t.Parallel()
	root := fixtureRoot(t)

	t.Run("optional-disabled-first", func(t *testing.T) {
		t.Parallel()
		checkOptionalDisabledFirstProbe(t, root)
	})

	t.Run("required-narrowed-outcome-fallback", func(t *testing.T) {
		t.Parallel()
		checkRequiredNumericProbe(t, root)
	})
}

func checkOptionalDisabledFirstProbe(t *testing.T, root string) {
	t.Helper()
	model, err := loadModel(filepath.Join(root, "fixtureoptv1", "entitytype.json"))
	if err != nil {
		t.Fatal(err)
	}
	probe, err := selectCatalogProbe(model, newCatalogSchemaChecker())
	if err != nil {
		t.Fatal(err)
	}
	if len(probe.operations) != 1 {
		t.Fatalf("operations = %d, want 1", len(probe.operations))
	}
	operation := probe.operations[0]
	if catalogSupportsEqual(probe.support, operation.support) {
		t.Fatalf("operation support %s reuses the disabled first-case support", operation.support)
	}
	if string(probe.unequalState) != `{"code": 100, "note": "armed", "history": [90, 100]}` {
		t.Fatalf("unequal State = %s, want the valid State from the subsequent enabled case", probe.unequalState)
	}
	if string(operation.support) != `{"state": {"maximum": 900}, "operations": {"activate": {"label": "main"}}}` {
		t.Fatalf("operation support = %s, want the originating enabled support", operation.support)
	}
}

func checkRequiredNumericProbe(t *testing.T, root string) {
	t.Helper()
	model, err := loadModel(filepath.Join(root, "fixturereqv1", "entitytype.json"))
	if err != nil {
		t.Fatal(err)
	}
	probe, err := selectCatalogProbe(model, newCatalogSchemaChecker())
	if err != nil {
		t.Fatal(err)
	}
	if string(probe.validState) != `75.0` {
		t.Fatalf("valid State = %s, want 75.0", probe.validState)
	}
	// The 7.5e1 sibling is numerically equal to 75.0, so exact numeric
	// comparison must skip it; the narrowed recorded outcome 85 remains
	// the unequal probe.
	equivalent, err := equalCatalogJSON(probe.validState, json.RawMessage(`7.5e1`))
	if err != nil {
		t.Fatal(err)
	}
	if !equivalent {
		t.Fatalf("75.0 and 7.5e1 compare unequal, want numeric equivalent")
	}
	if string(probe.unequalState) != `85` {
		t.Fatalf("unequal State = %s, want the narrowed recorded outcome 85", probe.unequalState)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, destination, string(raw))
}

func copyDir(t *testing.T, source, destination string) {
	t.Helper()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		target := filepath.Join(destination, entry.Name())
		if entry.IsDir() {
			copyDir(t, filepath.Join(source, entry.Name()), target)
			continue
		}
		copyFile(t, filepath.Join(source, entry.Name()), target)
	}
}
