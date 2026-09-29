package zwavejs_test

import (
	"strings"
	"testing"

	appzwavejs "github.com/mholtzscher/hearth/internal/app/zwavejs"
)

// validConfig is the smallest accepted configuration, so each table case can vary exactly one field.
func validConfig() appzwavejs.Config {
	return appzwavejs.Config{
		AdapterID: "zwavejs",
		NATSURL:   "nats://127.0.0.1:4222",
		ZWaveJS:   appzwavejs.ZWaveJSConfig{URL: "ws://127.0.0.1:3000"},
	}
}

// Trusted endpoints reject unsupported schemes, credentials, paths, queries, fragments, and ports.
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
		// A rejected URL may carry credentials or a token, so the failure must never echo it.
		if rawURL != "" &&
			(strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), rawURL)) {
			t.Errorf("zwave_js.url %q leaked into error %v", rawURL, err)
		}
	}
}

// Adapter IDs must be safe for NATS subjects.
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

// NATS URLs must use the NATS scheme and be absolute.
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
