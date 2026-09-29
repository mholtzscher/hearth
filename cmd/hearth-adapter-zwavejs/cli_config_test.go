package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/app/zwavejs"
)

const validYAML = "adapter_id: zwavejs\nnats_url: nats://127.0.0.1:4222\nzwave_js:\n  url: ws://127.0.0.1:3000\n"

func TestExampleConfig(t *testing.T) {
	t.Parallel()
	got := captureConfig(t, []string{"--config", filepath.Join("..", "..", "configs", "zwavejs.example.yaml")})
	if got.AdapterID != "zwavejs" || got.NATSURL != "nats://127.0.0.1:4222" ||
		got.ZWaveJS.URL != "ws://127.0.0.1:3000" {
		t.Fatalf("example config = %#v", got)
	}
}

// Flags take precedence over environment and YAML settings.
func TestConfigSourcePrecedence(t *testing.T) {
	path := writeConfig(t, validYAML)
	t.Setenv("HEARTH_ADAPTER_ZWAVEJS_CONFIG", path)
	t.Setenv("HEARTH_ADAPTER_ZWAVEJS_ADAPTER_ID", "from-env")
	t.Setenv("HEARTH_ADAPTER_ZWAVEJS_NATS_URL", "nats://127.0.0.1:4223")
	t.Setenv("HEARTH_ADAPTER_ZWAVEJS_ZWAVE_JS_URL", "ws://127.0.0.1:3001")
	got := captureConfig(t, nil)
	if got.AdapterID != "from-env" || got.NATSURL != "nats://127.0.0.1:4223" ||
		got.ZWaveJS.URL != "ws://127.0.0.1:3001" {
		t.Fatalf("environment did not override YAML: %#v", got)
	}
	other := writeConfig(t, strings.Replace(validYAML, "adapter_id: zwavejs", "adapter_id: other", 1))
	got = captureConfig(
		t,
		[]string{
			"--config",
			other,
			"--adapter-id",
			"from-flag",
			"--nats-url",
			"nats://127.0.0.1:4224",
			"--zwave-js-url",
			"ws://127.0.0.1:3002",
		},
	)
	if got.AdapterID != "from-flag" || got.NATSURL != "nats://127.0.0.1:4224" ||
		got.ZWaveJS.URL != "ws://127.0.0.1:3002" {
		t.Fatalf("flags did not override environment: %#v", got)
	}
}

// A missing implicit YAML file must not prevent flags from supplying all required settings.
func TestFlagOnlyConfigWithoutDefaultYAML(t *testing.T) {
	t.Parallel()
	got := captureConfig(
		t,
		[]string{
			"--adapter-id",
			"zwavejs",
			"--nats-url",
			"nats://127.0.0.1:4224",
			"--zwave-js-url",
			"ws://127.0.0.1:3002",
		},
	)
	if got.AdapterID != "zwavejs" || got.NATSURL != "nats://127.0.0.1:4224" ||
		got.ZWaveJS.URL != "ws://127.0.0.1:3002" {
		t.Fatalf("configuration without YAML = %#v", got)
	}
}

// The optional decoder ignores unknown keys but rejects malformed or multiple documents. Final endpoint validation must
// still run after overrides.
func TestYAMLPolicyAndValidation(t *testing.T) {
	t.Parallel()
	if got := captureConfig(
		t,
		[]string{"--config", writeConfig(t, validYAML+"future: ignored\n")},
	); got.ZWaveJS.URL != "ws://127.0.0.1:3000" {
		t.Fatalf("YAML config = %#v", got)
	}
	for _, contents := range []string{validYAML + "---\n" + validYAML, "zwave_js: [\n", validYAML + "zwave_js:\n  url: ws://127.0.0.1:3000\n"} {
		if _, err := resolvedForTest(t, []string{"--config", writeConfig(t, contents)}); err == nil {
			t.Errorf("accepted malformed YAML %q", contents)
		}
	}
	path := writeConfig(t, strings.Replace(validYAML, "ws://127.0.0.1:3000", "wss://127.0.0.1:3000", 1))
	if _, err := resolvedForTest(t, []string{"--config", path}); err == nil {
		t.Fatal("accepted TLS endpoint")
	}
	if got := captureConfig(
		t,
		[]string{"--config", path, "--zwave-js-url", "ws://127.0.0.1:3000"},
	); got.ZWaveJS.URL != "ws://127.0.0.1:3000" {
		t.Fatalf("override did not repair invalid YAML endpoint: %#v", got)
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "zwavejs.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureConfig(t *testing.T, args []string) zwavejs.Config {
	t.Helper()
	got, err := resolvedForTest(t, args)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func resolvedForTest(t *testing.T, args []string) (zwavejs.Config, error) {
	t.Helper()
	var got zwavejs.Config
	cmd := newCommand(func(_ context.Context, config zwavejs.Config, _ *slog.Logger) error {
		got = config
		return nil
	}, io.Discard)
	err := cmd.Run(context.Background(), append([]string{"hearth-adapter-zwavejs"}, args...))
	return got, err
}
