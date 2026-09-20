package zwavejs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appzwavejs "github.com/mholtzscher/hearth/internal/app/zwavejs"
)

// writeConfigFile writes one YAML document to a temporary file.
func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "zwavejs.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// validConfig is the smallest accepted configuration, so each table case can
// vary exactly one field.
func validConfig() appzwavejs.Config {
	return appzwavejs.Config{
		AdapterID: "zwavejs",
		NATSURL:   "nats://127.0.0.1:4222",
		ZWaveJS:   appzwavejs.ZWaveJSConfig{URL: "ws://127.0.0.1:3000"},
	}
}

// This test protects the checked-in example and fails if it stops naming a
// loopback Z-Wave JS server, because v1 has no authentication or TLS and the
// endpoint must never be exposed to an untrusted network.
func TestLoadExampleConfig(t *testing.T) {
	t.Parallel()
	value, err := appzwavejs.LoadConfig(filepath.Join("..", "..", "..", "configs", "zwavejs.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if value.AdapterID != "zwavejs" {
		t.Fatalf("adapter_id = %q, want zwavejs", value.AdapterID)
	}
	if value.NATSURL != "nats://127.0.0.1:4222" {
		t.Fatalf("nats_url = %q, want nats://127.0.0.1:4222", value.NATSURL)
	}
	if value.ZWaveJS.URL != "ws://127.0.0.1:3000" {
		t.Fatalf("zwave_js.url = %q, want ws://127.0.0.1:3000", value.ZWaveJS.URL)
	}
}

// This test protects the trusted-endpoint contract and fails if a scheme,
// credential, path, query, fragment, or port variant is accepted.
func TestConfigValidateZWaveJSServerURL(t *testing.T) {
	t.Parallel()
	accepted := []string{
		"ws://127.0.0.1:3000",
		"ws://127.0.0.1:3000/",
		"ws://zwavejs-ui.lan:3000",
		"ws://192.168.10.4:8080",
		"ws://[::1]:3000",
	}
	for _, rawURL := range accepted {
		value := validConfig()
		value.ZWaveJS.URL = rawURL
		if err := value.Validate(); err != nil {
			t.Errorf("zwave_js.url %q unexpectedly rejected: %v", rawURL, err)
		}
	}

	rejected := []string{
		"",
		"127.0.0.1:3000",
		"ws://127.0.0.1",
		"ws://:3000",
		"ws://127.0.0.1:",
		"ws://127.0.0.1:0",
		"ws://127.0.0.1:65536",
		"ws://127.0.0.1:notaport",
		"wss://127.0.0.1:3000",
		"http://127.0.0.1:3000",
		"https://127.0.0.1:3000",
		"WS://127.0.0.1:3000",
		"Ws://127.0.0.1:3000",
		"ws://user@127.0.0.1:3000",
		"ws://user:secret@127.0.0.1:3000",
		"ws://127.0.0.1:3000/ws",
		"ws://127.0.0.1:3000/zwave",
		"ws://127.0.0.1:3000/?token=secret",
		"ws://127.0.0.1:3000?",
		"ws://127.0.0.1:3000#fragment",
		"ws://127.0.0.1:3000/#",
	}
	for _, rawURL := range rejected {
		value := validConfig()
		value.ZWaveJS.URL = rawURL
		err := value.Validate()
		if err == nil {
			t.Errorf("zwave_js.url %q unexpectedly accepted", rawURL)
			continue
		}
		// A rejected URL may carry credentials or a token, so the failure must
		// never echo it.
		if rawURL != "" &&
			(strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), rawURL)) {
			t.Errorf("zwave_js.url %q leaked into error %v", rawURL, err)
		}
	}
}

// This test protects Hearth's subject-safe Adapter identity and fails if a
// value that cannot name NATS subjects is accepted.
func TestConfigValidateRequiresAdapterSlug(t *testing.T) {
	t.Parallel()
	for _, adapterID := range []string{
		"", "ZwaveJS", "zwave.js", "zwave js", "-zwavejs", "zwavejs!", strings.Repeat("z", 64),
	} {
		value := validConfig()
		value.AdapterID = adapterID
		if err := value.Validate(); err == nil {
			t.Errorf("adapter_id %q unexpectedly accepted", adapterID)
		}
	}
	for _, adapterID := range []string{"zwavejs", "zwave-js", "zwave_js_2"} {
		value := validConfig()
		value.AdapterID = adapterID
		if err := value.Validate(); err != nil {
			t.Errorf("adapter_id %q unexpectedly rejected: %v", adapterID, err)
		}
	}
}

// This test protects the existing NATS URL contract and fails if a non-NATS or
// relative URL is accepted.
func TestConfigValidateRequiresNATSURL(t *testing.T) {
	t.Parallel()
	for _, natsURL := range []string{"", "127.0.0.1:4222", "http://127.0.0.1:4222", "tls://127.0.0.1:4222"} {
		value := validConfig()
		value.NATSURL = natsURL
		if err := value.Validate(); err == nil {
			t.Errorf("nats_url %q unexpectedly accepted", natsURL)
		}
	}
}

// This test protects the strict YAML contract and fails if an unknown field or a
// second document is silently ignored.
func TestLoadConfigIsStrict(t *testing.T) {
	t.Parallel()
	valid := "adapter_id: zwavejs\nnats_url: nats://127.0.0.1:4222\nzwave_js:\n  url: ws://127.0.0.1:3000\n"
	loaded, err := appzwavejs.LoadConfig(writeConfigFile(t, valid))
	if err != nil {
		t.Fatal(err)
	}
	if loaded != validConfig() {
		t.Fatalf("loaded config = %#v, want %#v", loaded, validConfig())
	}

	invalid := []string{
		valid + "unknown: true\n",
		valid + "zwave_js:\n  url: ws://127.0.0.1:3000\n  token: secret\n",
		valid + "---\n" + valid,
		"adapter_id: zwavejs\nnats_url: nats://127.0.0.1:4222\n",
	}
	for _, contents := range invalid {
		if _, err = appzwavejs.LoadConfig(writeConfigFile(t, contents)); err == nil {
			t.Errorf("config unexpectedly accepted: %q", contents)
		}
	}
}

// This test protects the load-time validation path and fails if a syntactically
// valid file with an unusable endpoint is loaded.
func TestLoadConfigValidatesEndpoint(t *testing.T) {
	t.Parallel()
	contents := "adapter_id: zwavejs\nnats_url: nats://127.0.0.1:4222\nzwave_js:\n  url: wss://zwavejs-ui.lan:3000\n"
	_, err := appzwavejs.LoadConfig(writeConfigFile(t, contents))
	if err == nil {
		t.Fatal("TLS endpoint unexpectedly accepted")
	}
	if !strings.Contains(err.Error(), "zwave_js.url") {
		t.Fatalf("error %v does not name zwave_js.url", err)
	}
}
