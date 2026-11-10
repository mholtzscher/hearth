package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/app/pirateweather"
)

func TestCLIConfigOverridesAndSafeFailures(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "weather.yaml")
	if err := os.WriteFile(
		path,
		[]byte(
			"adapter_id: weather\napi_key_file: /private/key\nlocation:\n  name: Home\n  latitude: 0\n  longitude: 0\n",
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	called := false
	command := newCommand(func(_ context.Context, config pirateweather.Config, _ *slog.Logger) error {
		called = true
		if config.AdapterID != "weather-home" || config.NATSURL != "nats://127.0.0.1:4222" {
			t.Fatal(config)
		}
		return errors.New("sensitive secret provider error")
	}, &logs)
	err := command.Run(
		t.Context(),
		[]string{"weather", "--config", path, "--adapter-id", "weather-home", "--log-format", "json"},
	)
	if !called || !errors.Is(err, errAdapterFailed) || !strings.Contains(logs.String(), "run_failed") ||
		strings.Contains(logs.String(), "sensitive") ||
		strings.Contains(logs.String(), "/private/key") {
		t.Fatalf("result=%v logs=%s", err, logs.String())
	}
}

func TestCLIRejectsInvalidLoggingBeforeConfig(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	called := false
	command := newCommand(
		func(context.Context, pirateweather.Config, *slog.Logger) error { called = true; return nil },
		&logs,
	)
	err := command.Run(t.Context(), []string{"weather", "--config", "/missing", "--log-format", "bad"})
	if err == nil || called || strings.Contains(logs.String(), "process.starting") {
		t.Fatalf("%v %s", err, logs.String())
	}
}
