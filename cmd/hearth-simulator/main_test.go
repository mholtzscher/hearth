package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/cmd/internal/cmdtest"
)

// validDevicesConfig is a minimal scripted Device list that satisfies the
// authoritative Entity-type schemas, so it only fails when it should.
const validDevicesConfig = "adapter_id: \"test-simulator\"\n" +
	"nats_url: \"nats://127.0.0.1:4222\"\n" +
	"devices:\n" +
	"  - binding_key: \"test-light\"\n" +
	"    name: \"Test light\"\n" +
	"    kind: \"light\"\n" +
	"    entities:\n" +
	"      - key: \"power\"\n" +
	"        name: \"Power\"\n" +
	"        type: \"hearth.power/v1\"\n" +
	"        support: {state: {}, operations: {set: {}}}\n" +
	"        initial: true\n"

// invalidDevicesConfig omits the colorhs set operation the type requires.
const invalidDevicesConfig = "adapter_id: \"test-simulator\"\n" +
	"nats_url: \"nats://127.0.0.1:4222\"\n" +
	"devices:\n" +
	"  - binding_key: \"test-light\"\n" +
	"    name: \"Test light\"\n" +
	"    kind: \"light\"\n" +
	"    entities:\n" +
	"      - key: \"color\"\n" +
	"        name: \"Color\"\n" +
	"        type: \"hearth.colorhs/v1\"\n" +
	"        support: {state: {}, operations: {}}\n" +
	"        initial: {active: true, hue: 120, saturation: 80}\n"

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "simulator.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// This test protects executable bootstrap and fails if invalid logging flags
// do not fail fast before configuration load, echo the rejected value, or
// misreport the configuration failure in either format.
func TestMainLoggingFlagsAndConfigFailure(t *testing.T) {
	t.Parallel()
	binary := cmdtest.Build(t, ".")

	t.Run("invalid flags", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckInvalidLogFlags(t, binary)
	})
	t.Run("missing config text", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckMissingConfigText(t, binary)
	})
	t.Run("missing config json", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckMissingConfigJSON(t, binary, "hearth-simulator")
	})
	t.Run("invalid config detail", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckInvalidConfigDetail(t, binary, "hearth-simulator", invalidDevicesConfig)
	})
	t.Run("startup cancellation", func(t *testing.T) {
		t.Parallel()
		natsURL := cmdtest.StartProcessNATS(t)
		configYAML := strings.Replace(validDevicesConfig,
			"nats://127.0.0.1:4222", natsURL, 1)
		cmdtest.CheckStartupCancellation(t, binary, configYAML, "hearth-simulator")
	})
}

// This test protects the pre-launch configuration check the simulator
// validation harness calls before creating a Herdr tab: a valid Device list
// exits zero, and an invalid one exits nonzero naming the offending Device and
// Entity without starting the process lifecycle.
func TestValidateConfigFlag(t *testing.T) {
	t.Parallel()
	binary := cmdtest.Build(t, ".")

	t.Run("valid devices", func(t *testing.T) {
		t.Parallel()
		path := writeConfig(t, validDevicesConfig)
		result := cmdtest.Run(t, binary, "--config", path, "--validate-config")
		if result.ExitCode != 0 {
			t.Fatalf("valid config exited %d: %q", result.ExitCode, result.Stderr)
		}
		if !strings.Contains(result.Stdout, "configuration valid") {
			t.Fatalf("valid config stdout = %q", result.Stdout)
		}
	})
	t.Run("invalid devices", func(t *testing.T) {
		t.Parallel()
		path := writeConfig(t, invalidDevicesConfig)
		result := cmdtest.Run(t, binary, "--config", path, "--validate-config")
		if result.ExitCode == 0 {
			t.Fatalf("invalid config exited 0: %q", result.Stdout)
		}
		for _, want := range []string{"devices[0] entities[0]", "invalid support"} {
			if !strings.Contains(result.Stderr, want) {
				t.Fatalf("stderr lacks %q: %q", want, result.Stderr)
			}
		}
		if strings.Contains(result.Stderr, "process.starting") {
			t.Fatalf("validation started the process lifecycle: %q", result.Stderr)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "missing.yaml")
		result := cmdtest.Run(t, binary, "--config", missing, "--validate-config")
		if result.ExitCode == 0 {
			t.Fatalf("missing config exited 0: %q", result.Stdout)
		}
	})
}
