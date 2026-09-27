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

// The old mode switch is no longer a supported CLI option.
func TestValidateConfigFlagRejected(t *testing.T) {
	t.Parallel()
	binary := cmdtest.Build(t, ".")
	result := cmdtest.Run(t, binary, "--validate-config")
	if result.ExitCode == 0 || !strings.Contains(result.Stderr, "validate-config") {
		t.Fatalf("removed flag accepted or not reported: exit %d, stderr %q", result.ExitCode, result.Stderr)
	}
}
