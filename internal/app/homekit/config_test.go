package homekit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/app/homekit"
)

func TestConfigRejectsInvalidSettings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		field  string
		change func(*homekit.Config)
	}{
		{"adapter_id", func(c *homekit.Config) { c.AdapterID = "Bad ID" }},
		{"nats_url", func(c *homekit.Config) { c.NATSURL = new("https://localhost:4222") }},
		{"core_url", func(c *homekit.Config) { c.CoreURL = "http://localhost:8080/v1" }},
		{"core_url", func(c *homekit.Config) { c.CoreURL = "http://user:password@localhost:8080" }},
		{"data_dir", func(c *homekit.Config) { c.DataDir = "relative" }},
		{"bridge_name", func(c *homekit.Config) { c.BridgeName = new(" ") }},
		{"listen_port", func(c *homekit.Config) { c.ListenPort = new(65536) }},
		{"resync_interval", func(c *homekit.Config) { c.ResyncInterval = new(14 * time.Second) }},
		{"temperature_display_unit", func(c *homekit.Config) { c.TemperatureDisplayUnit = new("kelvin") }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			cfg := homekit.Config{
				AdapterID: "homekit", CoreURL: "http://127.0.0.1:8080", DataDir: t.TempDir(),
			}
			tc.change(&cfg)
			_, err := cfg.Resolve()
			require.ErrorContains(t, err, tc.field)
			assert.NotContains(t, err.Error(), "password")
		})
	}
}
