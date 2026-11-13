package esphome //nolint:testpackage // Tests the config-to-runtime preparation boundary.

import (
	"testing"

	esphomeadapter "github.com/mholtzscher/hearth/internal/adapters/esphome"
)

func TestDiscoveryOnlyConfigAndAddresslessOverrides(t *testing.T) {
	t.Parallel()
	config := Config{AdapterID: "esphome", NATSURL: "nats://127.0.0.1:4222",
		Discovery: DiscoveryConfig{Enabled: true, AllowInsecurePlaintext: true}}
	prepared, err := adapterConfig(config)
	if err != nil || !prepared.Discovery.Enabled || !prepared.Discovery.AllowInsecurePlaintext ||
		len(prepared.Static) != 0 || len(prepared.Overrides) != 0 {
		t.Fatalf("discovery-only = %+v, %v", prepared, err)
	}
	config.Devices = []DeviceConfig{{MAC: "02:00:00:00:00:01", Exclude: []string{"light/RGB Light"}}}
	prepared, err = adapterConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	mac, _ := esphomeadapter.ParseMAC(config.Devices[0].MAC)
	device, found := prepared.Overrides[mac]
	if !found || len(prepared.Static) != 0 || device.Port != 0 || device.Transport != (esphomeadapter.Transport{}) ||
		len(device.Exposure.Exclude) != 1 {
		t.Fatalf("override did not inherit endpoint: %+v", device)
	}
}

func TestDiscoveryBlocklistWinsBeforeOpeningKeys(t *testing.T) {
	t.Parallel()
	config := validConfig()
	config.Blocklist = []string{config.Devices[0].MAC}
	config.Devices[0].EncryptionKeyFile = "/must/not/be/opened"
	prepared, err := adapterConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	mac, _ := esphomeadapter.ParseMAC(config.Blocklist[0])
	if _, blocked := prepared.Blocklist[mac]; !blocked || len(prepared.Static) != 0 || len(prepared.Overrides) != 0 {
		t.Fatalf("blocklist lost: %+v", prepared)
	}
}

func TestPreparationSeparatesStaticEndpointsAndOverrides(t *testing.T) {
	t.Parallel()
	apiPort := 6054
	config := Config{AdapterID: "esphome", NATSURL: "nats://127.0.0.1:4222",
		Discovery: DiscoveryConfig{Enabled: true},
		Devices: []DeviceConfig{
			{MAC: "02:00:00:00:00:01", Address: "192.0.2.10", InsecurePlaintext: true},
			{
				MAC:               "02:00:00:00:00:02",
				Port:              &apiPort,
				InsecurePlaintext: true,
				Include:           []string{"sensor/Temperature"},
			},
		},
	}
	prepared, err := adapterConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Static) != 1 || len(prepared.Overrides) != 1 {
		t.Fatalf("classification = %+v", prepared)
	}
	static := prepared.Static[0]
	if static.Address != "192.0.2.10" || static.Port != 6053 ||
		static.Transport != esphomeadapter.PlaintextTransport() {
		t.Fatalf("static endpoint = %+v", static)
	}
	mac, _ := esphomeadapter.ParseMAC(config.Devices[1].MAC)
	override := prepared.Overrides[mac]
	if override.Port != 6054 || override.Transport != esphomeadapter.PlaintextTransport() ||
		len(override.Exposure.Include) != 1 {
		t.Fatalf("override = %+v", override)
	}
}

func TestDiscoveryConfigRejectsAmbiguousAuthorizationAndBadBlocklist(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"permission without discovery", func(c *Config) { c.Discovery.AllowInsecurePlaintext = true }},
		{"addressless without discovery", func(c *Config) { c.Devices[0].Address = "" }},
		{"bad blocked MAC", func(c *Config) { c.Blocklist = []string{"not-a-mac"} }},
		{"duplicate blocked MAC", func(c *Config) { c.Blocklist = []string{"aa:00:00:00:00:01", "AA:00:00:00:00:01"} }},
		{"key and plaintext", func(c *Config) {
			c.Discovery.Enabled = true
			c.Devices[0].Address = ""
			c.Devices[0].InsecurePlaintext = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := validConfig()
			tc.change(&config)
			if err := config.Validate(); err == nil {
				t.Fatal("invalid discovery config accepted")
			}
		})
	}
}
