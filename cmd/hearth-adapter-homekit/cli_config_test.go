package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/app/homekit"
)

func TestHomeKitCommandResolvesConfigurationSources(t *testing.T) {
	path := writeConfig(t, `adapter_id: homekit-west
core_url: http://127.0.0.1:8080
data_dir: /var/lib/homekit-test
bridge_name: YAML bridge
interfaces: [lan0]
listen_port: 21100
resync_interval: 45s
`)
	t.Setenv("HEARTH_ADAPTER_HOMEKIT_BRIDGE_NAME", "Environment bridge")
	t.Setenv("HEARTH_ADAPTER_HOMEKIT_LISTEN_PORT", "21101")

	var output bytes.Buffer
	var got homekit.Config
	command := newCommand(func(_ context.Context, c homekit.Config, _ *slog.Logger) error {
		got = c
		return nil
	}, &output)
	err := command.Run(t.Context(), []string{homekit.SoftwareName, "--config", path, "--listen-port", "21102"})
	require.NoError(t, err, output.String())
	assert.Equal(t, "homekit-west", got.AdapterID)
	assert.Equal(t, "http://127.0.0.1:8080", got.CoreURL)
	assert.Equal(t, "/var/lib/homekit-test", got.DataDir)
	assert.Equal(t, "Environment bridge", *got.BridgeName)
	assert.Equal(t, 21102, *got.ListenPort)
	assert.Equal(t, []string{"lan0"}, got.Interfaces)
	assert.Equal(t, 45*time.Second, *got.ResyncInterval)
	assert.Equal(t, "nats://127.0.0.1:4222", *got.NATSURL)
	assert.Equal(t, "celsius", *got.TemperatureDisplayUnit)
}

func TestHomeKitCommandRejectsExplicitZeroPort(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, `adapter_id: homekit-west
core_url: http://127.0.0.1:8080
data_dir: /var/lib/homekit-test
listen_port: 0
`)
	var output bytes.Buffer
	command := newCommand(func(context.Context, homekit.Config, *slog.Logger) error {
		t.Fatal("invalid config reached the Adapter runtime")
		return nil
	}, &output)
	require.ErrorIs(t, command.Run(t.Context(), []string{homekit.SoftwareName, "--config", path}), errAdapterFailed)
	assert.Contains(t, output.String(), "listen_port")
}

func writeConfig(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "homekit.yaml")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
	return path
}
