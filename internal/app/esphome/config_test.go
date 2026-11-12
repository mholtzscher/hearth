package esphome //nolint:testpackage // Tests exercise configuration preparation and supervision seams.

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{AdapterID: "esphome", NATSURL: "nats://127.0.0.1:4222", Devices: []DeviceConfig{{
		MAC: "02:00:00:00:00:01", Address: "192.0.2.10", EncryptionKeyFile: "/not/read/by/validate",
	}}}
}

func TestValidateDevices(t *testing.T) {
	t.Parallel()
	port := func(value int) *int { return &value }
	for _, tc := range []struct {
		name   string
		change func(*Config)
		field  string
	}{
		{"empty", func(c *Config) { c.Devices = nil }, "devices"},
		{"too many", func(c *Config) { c.Devices = make([]DeviceConfig, 65) }, "devices"},
		{"duplicate", func(c *Config) {
			c.Devices[0].MAC = "AA:00:00:00:00:01"
			d := c.Devices[0]
			d.MAC = "aa:00:00:00:00:01"
			c.Devices = append(c.Devices, d)
		}, "mac"},
		{"compact MAC", func(c *Config) { c.Devices[0].MAC = "020000000001" }, "mac"},
		{"uneven MAC", func(c *Config) { c.Devices[0].MAC = "0:200:00:00:00:01" }, "mac"},
		{"local", func(c *Config) { c.Devices[0].Address = "secret.LOCAL." }, "address"},
		{"scheme", func(c *Config) { c.Devices[0].Address = "http://secret" }, "address"},
		{"port in address", func(c *Config) { c.Devices[0].Address = "secret:6053" }, "address"},
		{"path", func(c *Config) { c.Devices[0].Address = "secret/path" }, "address"},
		{"port zero", func(c *Config) { c.Devices[0].Port = port(0) }, "port"},
		{"port negative", func(c *Config) { c.Devices[0].Port = port(-1) }, "port"},
		{"port high", func(c *Config) { c.Devices[0].Port = port(65536) }, "port"},
		{"key required", func(c *Config) { c.Devices[0].EncryptionKeyFile = "" }, "encryption_key_file"},
		{"invalid include", func(c *Config) { c.Devices[0].Include = []string{"button/secret"} }, "include"},
		{"invalid exclude", func(c *Config) { c.Devices[0].Exclude = []string{"sensor/"} }, "exclude"},
		{"overlap", func(c *Config) {
			c.Devices[0].Include = []string{"light/Lamp"}
			c.Devices[0].Exclude = []string{"light/Lamp"}
		}, "include"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := validConfig()
			tc.change(&config)
			err := config.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("Validate error = %v, want field %s", err, tc.field)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked input: %v", err)
			}
		})
	}
	for _, address := range []string{"2001:db8::1", "::1", "example.lan.", "192.0.2.1"} {
		config := validConfig()
		config.Devices[0].Address = address
		if err := config.Validate(); err != nil {
			t.Errorf("valid address rejected: %v", err)
		}
	}
}

//nolint:gocognit,paralleltest,tparallel // Sequential key-file states intentionally reuse one path, including the missing file.
func TestAdapterConfigKeyFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	path := filepath.Join(dir, "secret-path")
	config := validConfig()
	config.Devices[0].EncryptionKeyFile = path
	config.Devices[0].MAC = "AA:BB:CC:DD:EE:FF"
	config.Devices[0].Include = []string{"sensor/Temperature"}
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate should not read key: %v", err)
	}
	for _, tc := range []struct {
		name, content string
		write         bool
	}{
		{"missing", "", false}, {"malformed", "secret-content", true}, {"oversize", strings.Repeat("x", 4097), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.write {
				if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := adapterConfig(config)
			if err == nil || !strings.Contains(err.Error(), "devices[0].encryption_key_file") {
				t.Fatalf("error = %v", err)
			}
			for _, secret := range []string{path, "secret-content", config.Devices[0].Address} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("key error leaked %q", secret)
				}
			}
		})
	}
	if err := os.WriteFile(path, []byte(" \n"+key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := adapterConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	device := got.Devices[0]
	if device.Port != 6053 || device.MAC.String() != "aa:bb:cc:dd:ee:ff" || len(device.Exposure.Include) != 1 {
		t.Fatalf("converted device: MAC=%s port=%d includes=%d", device.MAC, device.Port, len(device.Exposure.Include))
	}
}

func TestRunRejectsKeyBeforeInfrastructure(t *testing.T) {
	t.Parallel()
	config := validConfig()
	err := Run(context.Background(), config, nil)
	if err == nil || !strings.Contains(err.Error(), "devices[0].encryption_key_file") {
		t.Fatalf("Run error = %v", err)
	}
}

func TestSuperviseCancelsAndJoinsSibling(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	joined := make(chan struct{})
	failure := errors.New("adapter failed")
	err := supervise(context.Background(), func(context.Context) error {
		<-started
		return failure
	}, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(joined)
		return ctx.Err()
	})
	if !errors.Is(err, failure) {
		t.Fatalf("supervise error = %v", err)
	}
	select {
	case <-joined:
	default:
		t.Fatal("sibling not joined")
	}
}
