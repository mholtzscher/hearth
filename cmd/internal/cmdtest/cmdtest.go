// Package cmdtest runs built executables as subprocesses for
// executable-level contract tests: invalid logging flags fail before
// configuration load without echoing the supplied value, and configuration
// failures report process.failed with the required structured fields.
package cmdtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Result captures one executable subprocess run.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Build compiles the main package in dir and returns the binary path. The
// binary lives for the calling test's lifetime.
func Build(t *testing.T, dir string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "under-test")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	build.Dir = dir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", dir, err, output)
	}
	return binary
}

// Run executes the binary with args and captures its output. A nonzero exit
// is returned as data, not a test failure, so tests can assert on it.
func Run(t *testing.T, binary string, args ...string) Result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(t.Context(), binary, args...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	exitCode := 0
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("run %s: %v", binary, err)
		}
		exitCode = exitError.ExitCode()
	}
	return Result{ExitCode: exitCode, Stdout: stdout.String(), Stderr: stderr.String()}
}

// CheckInvalidLogFlags proves invalid logging options fail before
// configuration load: the diagnostic lists the allowed values without echoing
// the rejected value, and the config stage is never reached.
func CheckInvalidLogFlags(t *testing.T, binary string) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	tests := []struct {
		name       string
		level      string
		format     string
		diagnostic string
		rejected   string
	}{
		{"invalid level", "VERBOSE", "text", "must be one of debug, info, warn, error", "VERBOSE"},
		{"invalid format", "info", "yaml", "must be one of text, json", "yaml"},
		{"uppercase level", "INFO", "text", "must be one of debug, info, warn, error", "INFO"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := Run(t, binary,
				"--config", missing, "--log-level", test.level, "--log-format", test.format)
			if result.ExitCode == 0 {
				t.Fatalf("invalid flags exited 0: %q", result.Stderr)
			}
			if !strings.Contains(result.Stderr, test.diagnostic) {
				t.Fatalf("diagnostic = %q, want %q", result.Stderr, test.diagnostic)
			}
			if strings.Contains(result.Stderr, test.rejected) {
				t.Fatalf("diagnostic echoed the rejected value: %q", result.Stderr)
			}
			if strings.Contains(result.Stderr, "load_config") {
				t.Fatalf("invalid flags reached config load: %q", result.Stderr)
			}
		})
	}
}

// CheckMissingConfigText proves a missing configuration file fails with the
// starting and failed process records in text output.
func CheckMissingConfigText(t *testing.T, binary string) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	result := Run(t, binary,
		"--config", missing, "--log-level", "info", "--log-format", "text")
	if result.ExitCode == 0 {
		t.Fatalf("missing config exited 0: %q", result.Stderr)
	}
	for _, want := range []string{"process.starting", "process.failed", "load_config"} {
		if !strings.Contains(result.Stderr, want) {
			t.Fatalf("text stderr lacks %q: %q", want, result.Stderr)
		}
	}
	if strings.Contains(result.Stderr, missing) {
		t.Fatalf("config failure repeated the path: %q", result.Stderr)
	}
}

// CheckMissingConfigJSON proves a missing configuration file fails with
// structured starting and failed records carrying the required fields.
func CheckMissingConfigJSON(t *testing.T, binary string, binaryName string) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	result := Run(t, binary,
		"--config", missing, "--log-level", "info", "--log-format", "json")
	if result.ExitCode == 0 {
		t.Fatalf("missing config exited 0: %q", result.Stderr)
	}
	if strings.Contains(result.Stderr, missing) {
		t.Fatalf("config failure repeated the path: %q", result.Stderr)
	}
	records := DecodeLogRecords(t, result.Stderr)
	starting := FindEvent(records, "process.starting")
	if starting == nil {
		t.Fatalf("missing process.starting in %#v", records)
	}
	failed := FindEvent(records, "process.failed")
	if failed == nil {
		t.Fatalf("missing process.failed in %#v", records)
	}
	for _, record := range []map[string]any{starting, failed} {
		RequireField(t, record, "app", binaryName)
		RequireField(t, record, "component", "process")
		if _, ok := record["pid"].(float64); !ok {
			t.Fatalf("record lacks numeric pid: %#v", record)
		}
	}
	RequireField(t, failed, "stage", "load_config")
	if _, ok := failed["error_code"].(string); !ok {
		t.Fatalf("process.failed lacks error_code: %#v", failed)
	}
	if starting["pid"] != failed["pid"] {
		t.Fatalf("starting and failed pids differ: %#v vs %#v", starting, failed)
	}
}

// CheckInvalidConfigDetail proves a configuration that was read but fails
// static validation reports the actionable, path-free reason on
// process.failed, so an operator sees which field or Device is wrong without
// the process record echoing the configuration path.
func CheckInvalidConfigDetail(t *testing.T, binary string, binaryName string, configYAML string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	result := Run(t, binary, "--config", path, "--log-level", "info", "--log-format", "json")
	if result.ExitCode == 0 {
		t.Fatalf("invalid config exited 0: %q", result.Stderr)
	}
	if strings.Contains(result.Stderr, path) {
		t.Fatalf("config failure repeated the path: %q", result.Stderr)
	}
	records := DecodeLogRecords(t, result.Stderr)
	failed := FindEvent(records, "process.failed")
	if failed == nil {
		t.Fatalf("missing process.failed in %#v", records)
	}
	RequireField(t, failed, "app", binaryName)
	RequireField(t, failed, "stage", "load_config")
	reason, ok := failed["error"].(string)
	if !ok || strings.TrimSpace(reason) == "" {
		t.Fatalf("process.failed lacks an actionable reason: %#v", failed)
	}
	if strings.Contains(reason, path) {
		t.Fatalf("error reason repeated the path: %q", reason)
	}
	if reason == "configuration could not be read or decoded" {
		t.Fatalf("decodable YAML must report a validation reason, got %q", reason)
	}
}

// DecodeLogRecords parses one JSON record per line; every line must parse
// because executable bootstrap output is fully written before exit.
func DecodeLogRecords(t *testing.T, output string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("stderr line is not JSON: %q", line)
		}
		records = append(records, record)
	}
	return records
}

// FindEvent returns the first record with the given event name, or nil.
func FindEvent(records []map[string]any, event string) map[string]any {
	for _, record := range records {
		if record["event"] == event {
			return record
		}
	}
	return nil
}

// RequireField fails the test when the record field is not the wanted string.
func RequireField(t *testing.T, record map[string]any, key string, want string) {
	t.Helper()
	value, ok := record[key].(string)
	if !ok || value != want {
		t.Fatalf("record field %q = %#v, want %q (record: %#v)", key, record[key], want, record)
	}
}
