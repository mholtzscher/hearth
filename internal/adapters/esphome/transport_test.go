package esphome //nolint:testpackage // Exercises runtime configuration at the independent Adapter boundary.

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
)

func TestAdapterRequiresConcreteStaticEndpointsAndUniqueConfiguration(t *testing.T) {
	t.Parallel()
	device := harnessDevice(t, "02:00:00:00:00:01", "192.0.2.10")
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"missing address", func(c *Config) { c.Static[0].Address = "" }},
		{"missing port", func(c *Config) { c.Static[0].Port = 0 }},
		{"unspecified transport", func(c *Config) { c.Static[0].Transport = Transport{} }},
		{"duplicate static", func(c *Config) { c.Static = append(c.Static, device) }},
		{"static override overlap", func(c *Config) { c.Overrides = map[MAC]DiscoveryOverride{device.MAC: {}} }},
		{"combined capacity", func(c *Config) {
			c.Overrides = make(map[MAC]DiscoveryOverride)
			for index := range maximumDevices {
				c.Overrides[MAC{2, 0, 0, 0, 1, byte(index)}] = DiscoveryOverride{}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := Config{Static: []Device{device}}
			tc.change(&config)
			if _, err := New(&fakeSession{}, config, nil, nil); err == nil {
				t.Fatal("invalid runtime configuration accepted")
			}
		})
	}
}

func TestExplicitZeroKeyRemainsEncryptedAndOverridePortWins(t *testing.T) {
	t.Parallel()
	record := discoveredFixture(t, "192.0.2.10")
	transport := EncryptedTransport(nativeapi.PSK{})
	a := &Adapter{config: Config{
		Discovery: DiscoveryConfig{Enabled: true, AllowInsecurePlaintext: true},
		Overrides: map[MAC]DiscoveryOverride{record.MAC: {Transport: transport, Port: 6054}},
	}}
	device, reason := a.discoveredDevice(record)
	if reason != "" || device.Transport != transport || device.Port != 6054 {
		t.Fatalf("key or port policy changed: reason=%s port=%d", reason, device.Port)
	}
}
