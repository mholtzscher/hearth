package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	// fixtureExecEnv gates the expensive nested-module execution so ordinary
	// `go test ./...` runs stay cheap. Run `mise run test-entitytype-fixtures`.
	fixtureExecEnv = "HEARTH_FIXTURE_EXEC"
	fixtureModule  = "fixtureexec.test"
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
// asserted here; TestFixtureExecution executes the generated catalog wiring
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

// TestFixtureExecution materializes the fixture matrix in an isolated module
// with a local replace to this repository, generates all outputs, and runs
// the generated tests in a bounded offline subprocess. It is skipped unless
// HEARTH_FIXTURE_EXEC is set; run `mise run test-entitytype-fixtures`.
func TestFixtureExecution(t *testing.T) {
	t.Parallel()
	if os.Getenv(fixtureExecEnv) == "" {
		t.Skipf("set %s=1 to execute generated fixtures (mise run test-entitytype-fixtures)", fixtureExecEnv)
	}
	root := repoRoot(t)
	temporary := t.TempDir()

	writeFixtureModule(t, root, temporary)
	if err := generateRoot(temporary, false); err != nil {
		t.Fatal(err)
	}
	tidyFixtureModule(t, temporary)
	runFixtureTests(t, temporary)
}

func writeFixtureModule(t *testing.T, root, temporary string) {
	t.Helper()
	goVersion := readGoVersion(t, root)
	writeFile(t, filepath.Join(temporary, "go.mod"), fmt.Sprintf(
		"module %s\n\ngo %s\n\nrequire github.com/mholtzscher/hearth v0.0.0\n\nreplace github.com/mholtzscher/hearth => %s\n",
		fixtureModule,
		goVersion,
		root,
	))
	copyDir(
		t,
		filepath.Join(root, "internal", "cmd", "entitytypegen", "testdata", "fixtures"),
		filepath.Join(temporary, "entitytypes"),
	)
	copyFile(
		t,
		filepath.Join(root, "entitytypes", "entitytype-manifest.schema.json"),
		filepath.Join(temporary, "entitytypes", "entitytype-manifest.schema.json"),
	)
	// Catalog behavior is a runtime copy of production code, so fixture
	// execution tracks it exactly; only the plain model types are scaffolded.
	copyFile(
		t,
		filepath.Join(root, "internal", "modules", "devices", "catalog.go"),
		filepath.Join(temporary, "internal", "modules", "devices", "catalog.go"),
	)
	copyFile(
		t,
		filepath.Join(root, "internal", "cmd", "entitytypegen", "testdata", "fixturemodule", "devices_model.go"),
		filepath.Join(temporary, "internal", "modules", "devices", "model.go"),
	)
	// Generated contract tests import the shared handwritten runner, so the
	// fixture module carries it along. It depends only on the standard
	// library, keeping offline module preparation intact.
	copyDir(
		t,
		filepath.Join(root, "internal", "entitytypetest"),
		filepath.Join(temporary, "internal", "entitytypetest"),
	)
}

func readGoVersion(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(raw)) {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" {
			return fields[1]
		}
	}
	t.Fatal("go.mod has no go directive")
	return ""
}

func tidyFixtureModule(t *testing.T, temporary string) {
	t.Helper()
	// Ordinary module preparation; the test run itself stays offline.
	command := exec.Command("go", "mod", "tidy")
	command.Dir = temporary
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, output)
	}
}

func runFixtureTests(t *testing.T, temporary string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout())
	defer cancel()
	command := exec.CommandContext(
		ctx,
		"go",
		"test",
		"-count=1",
		"-timeout=90s",
		"./entitytypes/...",
		"./sdk/...",
		"./internal/modules/devices/...",
	)
	command.Dir = temporary
	command.Env = append(os.Environ(), "GOPROXY=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture tests: %v\n%s", err, output)
	}
	t.Logf("fixture tests:\n%s", output)
}

func fixtureTimeout() time.Duration { return 4 * time.Minute }

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
