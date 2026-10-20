//go:build integration

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// Generated facade tests also import sdk/adapter/adaptertest, which the
	// replace directive resolves to this repository like sdk/adapter itself.
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
