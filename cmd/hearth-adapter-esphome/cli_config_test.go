package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/app/esphome"
)

const validYAML = "devices:\n  - mac: '02:00:00:00:00:01'\n    address: 192.0.2.10\n    encryption_key_file: /secret/key\n"

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "esphome.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureConfig(t *testing.T, args ...string) (esphome.Config, string, error) {
	t.Helper()
	var got esphome.Config
	var logs bytes.Buffer
	cmd := newCommand(
		func(_ context.Context, config esphome.Config, _ *slog.Logger) error { got = config; return nil },
		&logs,
	)
	err := cmd.Run(context.Background(), append([]string{"hearth-adapter-esphome"}, args...))
	return got, logs.String(), err
}

func TestConfigPrecedence(t *testing.T) {
	path := writeConfig(t, "adapter_id: from-yaml\nnats_url: nats://127.0.0.1:4223\nlog_format: json\n"+validYAML)
	got, logs, err := captureConfig(t, "--config", path)
	if err != nil || got.AdapterID != "from-yaml" || got.NATSURL != "nats://127.0.0.1:4223" ||
		!strings.Contains(logs, `"event":"process.starting"`) {
		t.Fatalf("YAML: config=%+v logs=%s err=%v", got, logs, err)
	}
	t.Setenv("HEARTH_ADAPTER_ESPHOME_CONFIG", path)
	t.Setenv("HEARTH_ADAPTER_ESPHOME_ADAPTER_ID", "from-env")
	t.Setenv("HEARTH_ADAPTER_ESPHOME_NATS_URL", "nats://127.0.0.1:4224")
	t.Setenv("HEARTH_ADAPTER_ESPHOME_LOG_FORMAT", "text")
	got, logs, err = captureConfig(t)
	if err != nil || got.AdapterID != "from-env" || got.NATSURL != "nats://127.0.0.1:4224" ||
		strings.Contains(logs, `"event":"process.starting"`) {
		t.Fatalf("environment: config=%+v logs=%s err=%v", got, logs, err)
	}
	got, logs, err = captureConfig(
		t,
		"--adapter-id",
		"from-flag",
		"--nats-url",
		"nats://127.0.0.1:4225",
		"--log-format",
		"json",
	)
	if err != nil || got.AdapterID != "from-flag" || got.NATSURL != "nats://127.0.0.1:4225" ||
		!strings.Contains(logs, `"event":"process.starting"`) {
		t.Fatalf("flags: config=%+v logs=%s err=%v", got, logs, err)
	}
}

func TestDefaultsAndInvalidStartup(t *testing.T) {
	t.Parallel()
	got, _, err := captureConfig(t, "--config", writeConfig(t, validYAML))
	if err != nil || got.AdapterID != "esphome" || got.NATSURL != "nats://127.0.0.1:4222" ||
		got.Devices[0].Port != nil {
		t.Fatalf("defaults: %+v %v", got, err)
	}
	for _, yaml := range []string{strings.Replace(validYAML, "192.0.2.10", "device.local", 1), validYAML + "    port: 0\n", validYAML + "---\n" + validYAML} {
		called := false
		cmd := newCommand(
			func(context.Context, esphome.Config, *slog.Logger) error { called = true; return nil },
			io.Discard,
		)
		if err = cmd.Run(
			context.Background(),
			[]string{"hearth-adapter-esphome", "--config", writeConfig(t, yaml)},
		); err == nil ||
			called {
			t.Fatalf("invalid config reached Run: err=%v called=%v", err, called)
		}
	}
	if _, _, missingErr := captureConfig(t, "--config", filepath.Join(t.TempDir(), "missing.yaml")); missingErr == nil {
		t.Fatal("missing explicit config accepted")
	}
}

func TestExampleConfig(t *testing.T) {
	t.Parallel()
	got, _, err := captureConfig(t, "--config", filepath.Join("..", "..", "configs", "esphome.example.yaml"))
	if err != nil || len(got.Devices) != 2 {
		t.Fatalf("example: %+v %v", got, err)
	}
}

func TestDiscoveryOnlyYAML(t *testing.T) {
	t.Parallel()
	path := writeConfig(
		t,
		"discovery:\n  enabled: true\n  allow_insecure_plaintext: true\nblocklist:\n  - '02:00:00:00:00:01'\n",
	)
	got, _, err := captureConfig(t, "--config", path)
	if err != nil || !got.Discovery.Enabled || !got.Discovery.AllowInsecurePlaintext || len(got.Blocklist) != 1 ||
		len(got.Devices) != 0 {
		t.Fatalf("discovery YAML = %+v, %v", got, err)
	}
}
