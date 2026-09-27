package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/app/simulator"
)

const simulatorYAML = `adapter_id: yaml-adapter
nats_url: nats://127.0.0.1:4222
control_addr: 127.0.0.1:8181
devices:
  - binding_key: test-light
    name: Test light
    kind: light
    entities:
      - key: power
        name: Power
        type: hearth.power/v1
        support: {state: {}, operations: {set: {}}}
        initial: true
future_setting: ignored
`

// TestSimulatorConfigSourcePrecedence protects CLI > environment > YAML for
// simulator scalar settings; it fails if any source order is changed or omitted.
func TestSimulatorConfigSourcePrecedence(t *testing.T) {
	path := writeConfig(t, simulatorYAML)
	t.Setenv("HEARTH_SIMULATOR_ADAPTER_ID", "env-adapter")
	t.Setenv("HEARTH_SIMULATOR_NATS_URL", "nats://127.0.0.1:4223")
	t.Setenv("HEARTH_SIMULATOR_CONTROL_ADDR", "127.0.0.1:8182")
	t.Setenv("HEARTH_SIMULATOR_CONFIG", path)

	got := invokeSimulatorConfig(t, []string{"--adapter-id", "flag-adapter", "--nats-url", "nats://127.0.0.1:4224"})
	if got.AdapterID != "flag-adapter" || got.NATSURL != "nats://127.0.0.1:4224" ||
		got.ControlAddr != "127.0.0.1:8182" || len(got.Devices) != 1 {
		t.Fatalf("CLI/env result = %#v", got)
	}
	got = invokeSimulatorConfig(t, nil)
	if got.AdapterID != "env-adapter" || got.NATSURL != "nats://127.0.0.1:4223" || got.ControlAddr != "127.0.0.1:8182" {
		t.Fatalf("env result = %#v", got)
	}
	unsetSimulatorEnv(t, "HEARTH_SIMULATOR_ADAPTER_ID")
	unsetSimulatorEnv(t, "HEARTH_SIMULATOR_NATS_URL")
	unsetSimulatorEnv(t, "HEARTH_SIMULATOR_CONTROL_ADDR")
	got = invokeSimulatorConfig(t, nil)
	if got.AdapterID != "yaml-adapter" || got.NATSURL != "nats://127.0.0.1:4222" ||
		got.ControlAddr != "127.0.0.1:8181" {
		t.Fatalf("YAML result = %#v", got)
	}
}

// TestSimulatorConfigFilePolicy protects optional implicit YAML, explicit file
// errors, permissive unknown keys, and parsing before overrides are applied.
func TestSimulatorConfigFilePolicy(t *testing.T) {
	for _, key := range []string{
		"HEARTH_SIMULATOR_CONFIG", "HEARTH_SIMULATOR_ADAPTER_ID",
		"HEARTH_SIMULATOR_NATS_URL", "HEARTH_SIMULATOR_CONTROL_ADDR",
	} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	// An implicit missing file is tolerated, then validation reports the
	// missing YAML-only scripted Device rather than a file-read failure.
	t.Setenv("HEARTH_SIMULATOR_ADAPTER_ID", "env-adapter")
	t.Setenv("HEARTH_SIMULATOR_NATS_URL", "nats://127.0.0.1:4222")
	if _, err := invokeSimulatorConfigErr(t, nil); err == nil || strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("implicit absent YAML was treated as a file error: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	if _, err := invokeSimulatorConfigErr(t, []string{"--config", missing}); err == nil {
		t.Fatal("explicit missing config accepted")
	}
	bad := writeConfig(t, "nats_url: [\n")
	if _, err := invokeSimulatorConfigErr(t, []string{
		"--config", bad, "--nats-url", "nats://127.0.0.1:4222",
	}); err == nil {
		t.Fatal("malformed YAML accepted when overridden")
	}
	unknown := writeConfig(t, simulatorYAML)
	if _, err := invokeSimulatorConfigErr(t, []string{"--config", unknown}); err != nil {
		t.Fatalf("unknown YAML field rejected: %v", err)
	}
}

func invokeSimulatorConfig(t *testing.T, args []string) simulator.Config {
	t.Helper()
	config, err := invokeSimulatorConfigErr(t, args)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func invokeSimulatorConfigErr(t *testing.T, args []string) (simulator.Config, error) {
	t.Helper()
	var config simulator.Config
	var stderr strings.Builder
	command := newSimulatorCommand(func(_ context.Context, got simulator.Config, _ *slog.Logger) error {
		config = got
		return nil
	}, io.Discard, &stderr)
	full := append([]string{"hearth-simulator"}, args...)
	err := command.Run(context.Background(), full)
	if err != nil && stderr.Len() != 0 {
		return config, fmt.Errorf("%w: %s", err, stderr.String())
	}
	return config, err
}

func unsetSimulatorEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func TestSimulatorValidationOutputDoesNotExposeInvalidValue(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "adapter_id: secret-adapter\nnats_url: invalid-secret-url\n")
	var stderr strings.Builder
	command := newSimulatorCommand(
		func(context.Context, simulator.Config, *slog.Logger) error { return nil }, io.Discard, &stderr,
	)
	err := command.Run(context.Background(), []string{"hearth-simulator", "--config", path, "--validate-config"})
	if err == nil {
		t.Fatal("invalid config accepted")
	}
	if strings.Contains(stderr.String(), "invalid-secret-url") || strings.Contains(stderr.String(), path) {
		t.Fatalf("validation output leaked value or path: %q", stderr.String())
	}
}
