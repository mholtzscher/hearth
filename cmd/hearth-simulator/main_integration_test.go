//go:build integration

package main

import (
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/cmd/internal/cmdtest"
)

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
