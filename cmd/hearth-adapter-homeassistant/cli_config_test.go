package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/internal/app/homeassistant"
)

func TestConfigSourcePrecedenceAndYAMLPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "adapter_id: homeassistant\nnats_url: nats://127.0.0.1:4222\nbinding: {key: light, device_name: Lamp, entity_id: light.lamp, entity_name: Lamp}\nhome_assistant: {url: http://127.0.0.1:8123, token_file: token}\nunknown_key: ignored\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEARTH_ADAPTER_HOMEASSISTANT_NATS_URL", "nats://127.0.0.1:4223")
	got := captureConfig(t, []string{"--config", path})
	if got.NATSURL != "nats://127.0.0.1:4223" {
		t.Fatalf("env did not override YAML: %q", got.NATSURL)
	}
	got = captureConfig(t, []string{"--config", path, "--nats-url", "nats://127.0.0.1:4224"})
	if got.NATSURL != "nats://127.0.0.1:4224" {
		t.Fatalf("flag did not override env: %q", got.NATSURL)
	}
	for _, invalid := range []string{"nats_url: [\n", "adapter_id: a\nadapter_id: b\n", "---\nadapter_id: a\n---\nadapter_id: b\n"} {
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := resolvedForTest(t, []string{"--config", path}); err == nil {
			t.Errorf("accepted invalid YAML %q", invalid)
		}
	}
}

func captureConfig(t *testing.T, args []string) homeassistant.Config {
	t.Helper()
	got, err := resolvedForTest(t, args)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func resolvedForTest(t *testing.T, args []string) (homeassistant.Config, error) {
	t.Helper()
	var got homeassistant.Config
	cmd := newCommand(
		func(_ context.Context, c homeassistant.Config, _ *slog.Logger) error { got = c; return nil },
		io.Discard,
	)
	full := append([]string{"hearth-adapter-homeassistant"}, args...)
	err := cmd.Run(context.Background(), full)
	return got, err
}
