//go:build integration

package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/cmd/internal/cmdtest"
)

func TestMainLoggingAndStartup(t *testing.T) {
	t.Parallel()
	binary := cmdtest.Build(t, ".")
	t.Run("invalid logging flags", func(t *testing.T) { t.Parallel(); cmdtest.CheckInvalidLogFlags(t, binary) })
	t.Run("missing config text", func(t *testing.T) { t.Parallel(); cmdtest.CheckMissingConfigText(t, binary) })
	t.Run(
		"missing config json",
		func(t *testing.T) { t.Parallel(); cmdtest.CheckMissingConfigJSON(t, binary, "hearth-adapter-esphome") },
	)
	t.Run("invalid config detail", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckInvalidConfigDetail(
			t,
			binary,
			"hearth-adapter-esphome",
			strings.Replace(validYAML, "192.0.2.10", "device.local", 1),
		)
	})
	t.Run("key failure is safe", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "secret-key-path")
		if err := os.WriteFile(path, []byte("sensitive-key-content"), 0o600); err != nil {
			t.Fatal(err)
		}
		configPath := filepath.Join(t.TempDir(), "config.yaml")
		config := strings.Replace(validYAML, "/secret/key", path, 1)
		if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		result := cmdtest.Run(t, binary, "--config", configPath, "--log-format", "json")
		if result.ExitCode == 0 || !strings.Contains(result.Stderr, "devices[0].encryption_key_file") {
			t.Fatalf("key failure: %+v", result)
		}
		for _, secret := range []string{path, "sensitive-key-content", "192.0.2.10"} {
			if strings.Contains(result.Stderr, secret) {
				t.Fatalf("key failure leaked input: %s", result.Stderr)
			}
		}
	})
	t.Run("startup cancellation", func(t *testing.T) {
		t.Parallel()
		keyPath := filepath.Join(t.TempDir(), "key")
		if err := os.WriteFile(
			keyPath,
			[]byte(base64.StdEncoding.EncodeToString(make([]byte, 32))),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		config := "nats_url: '" + cmdtest.StartProcessNATS(
			t,
		) + "'\n" + strings.Replace(
			validYAML,
			"/secret/key",
			keyPath,
			1,
		)
		cmdtest.CheckStartupCancellation(t, binary, config, "hearth-adapter-esphome")
	})
}
