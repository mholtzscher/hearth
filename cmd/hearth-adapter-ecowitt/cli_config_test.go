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

	"github.com/mholtzscher/hearth/internal/app/ecowitt"
)

func TestTypedUploadIntervalSources(t *testing.T) {
	pass := filepath.Join(t.TempDir(), "passkey")
	if err := os.WriteFile(pass, []byte("0123456789abcdef0123456789abcdef"), 0600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "adapter_id: ecowitt\nnats_url: nats://127.0.0.1:4222\nmqtt: {url: tcp://127.0.0.1:1883, topic: ecowitt/943cc64457a7}\nstation: {gateway_name: Gateway, outdoor_array_name: Array, passkey_file: " + pass + ", upload_interval_seconds: 16}\n"
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEARTH_ADAPTER_ECOWITT_STATION_UPLOAD_INTERVAL_SECONDS", "24")
	got := invoke(t, []string{"--config", p})
	if got.Station.UploadIntervalSeconds != 24 {
		t.Fatalf("env interval = %d", got.Station.UploadIntervalSeconds)
	}
	t.Setenv("HEARTH_ADAPTER_ECOWITT_STATION_UPLOAD_INTERVAL_SECONDS", "not-an-integer")
	got = invoke(t, []string{"--config", p, "--station-upload-interval-seconds", "32"})
	if got.Station.UploadIntervalSeconds != 32 {
		t.Fatalf("flag interval = %d", got.Station.UploadIntervalSeconds)
	}

	var stderr bytes.Buffer
	runCalled := false
	c := newCommand(func(_ context.Context, _ ecowitt.Config, _ *slog.Logger) error {
		runCalled = true
		return nil
	}, &stderr)
	err := c.Run(context.Background(), []string{"app", "--config", p})
	if err == nil {
		t.Fatal("invalid typed env value was accepted")
	}
	if runCalled {
		t.Fatal("run callback was called despite invalid typed env value")
	}
	if strings.Contains(stderr.String(), `"event":"process.failed"`) {
		t.Fatalf("CLI parsing failure emitted structured process.failed: %s", stderr.String())
	}
}

// CLI resolution must not access the PASSKEY before Run starts.
func TestCLIResolvesMissingPasskeyFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing-passkey")
	got := invoke(t, []string{
		"--adapter-id", "ecowitt", "--nats-url", "nats://127.0.0.1:4222",
		"--mqtt-url", "tcp://127.0.0.1:1883", "--mqtt-topic", "ecowitt/station",
		"--station-gateway-name", "Gateway", "--station-outdoor-array-name", "Array",
		"--station-passkey-file", path, "--station-upload-interval-seconds", "16",
	})
	if got.Station.PasskeyFile != path {
		t.Fatalf("passkey path = %q, want %q", got.Station.PasskeyFile, path)
	}
}
func invoke(t *testing.T, args []string) ecowitt.Config {
	t.Helper()
	var got ecowitt.Config
	c := newCommand(func(_ context.Context, v ecowitt.Config, _ *slog.Logger) error { got = v; return nil }, io.Discard)
	if err := c.Run(context.Background(), append([]string{"app"}, args...)); err != nil {
		t.Fatal(err)
	}
	return got
}
