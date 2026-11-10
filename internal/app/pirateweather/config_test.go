package pirateweather //nolint:testpackage // Verifies secret loading and pre-network assembly validation.

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func validConfig() Config {
	zero := 0.0
	return Config{AdapterID: "pirateweather-home", NATSURL: "nats://127.0.0.1:4222", APIKeyFile: "secret",
		Location: LocationConfig{Name: " Home Weather ", Latitude: &zero, Longitude: &zero}}
}

func TestConfigDefaultsPresenceAndBounds(t *testing.T) {
	t.Parallel()
	config := validConfig()
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	runtime, err := config.runtimeConfig()
	if err != nil || runtime.PollInterval != 15*time.Minute || runtime.LocationName != "Home Weather" ||
		runtime.Latitude != 0 {
		t.Fatalf("runtime: %+v %v", runtime, err)
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.Location.Latitude = nil }, func(c *Config) { c.Location.Longitude = nil },
		func(c *Config) { n := math.Inf(1); c.Location.Latitude = &n }, func(c *Config) { interval := time.Minute; c.PollInterval = &interval },
		func(c *Config) { c.APIKeyFile = "" }, func(c *Config) { c.AdapterID = "bad.id" },
	} {
		bad := validConfig()
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("accepted invalid config")
		}
	}
	var decoded Config
	if err = yaml.Unmarshal(
		[]byte(
			"adapter_id: weather\nnats_url: nats://127.0.0.1:4222\napi_key_file: secret\nlocation:\n  name: Home\n  latitude: 0\n  longitude: 0\npoll_interval: 30m\n",
		),
		&decoded,
	); err != nil {
		t.Fatal(err)
	}
	if decoded.Validate() != nil || decoded.PollInterval == nil || *decoded.PollInterval != 30*time.Minute {
		t.Fatal(decoded)
	}
}

func TestSecretAndPreflightSafety(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "private-secret")
	for _, input := range []string{"", " \n", "first\nsecond", strings.Repeat("x", maximumSecretBytes+1), string([]byte{0xff})} {
		if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
			t.Fatal(err)
		}
		config := validConfig()
		config.APIKeyFile = path
		err := Run(t.Context(), config, slog.New(slog.DiscardHandler))
		if err == nil || strings.Contains(err.Error(), path) {
			t.Fatalf("unsafe or absent preflight failure: %v", err)
		}
	}
	if _, err := readSecret(dir); err == nil {
		t.Fatal("accepted directory")
	}
	if err := os.WriteFile(path, []byte(" test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if key, err := readSecret(path); err != nil || key != "test-key" {
		t.Fatal(key, err)
	}
	config := validConfig()
	config.APIKeyFile = path
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Run(ctx, config, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestExplicitZeroPollIntervalIsRejected(t *testing.T) {
	t.Parallel()
	config := validConfig()
	if err := yaml.Unmarshal([]byte("poll_interval: 0s\n"), &config); err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(); err == nil {
		t.Fatal("explicit zero interval accepted")
	}
}
