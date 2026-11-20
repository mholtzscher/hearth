// Package homekit assembles the HomeKit Adapter process.
package homekit

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	bridge "github.com/mholtzscher/hearth/internal/adapters/homekit"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

// SoftwareName identifies the executable in Adapter Session and process records.
const SoftwareName = "hearth-adapter-homekit"

const defaultListenPort = 21063

// Config holds the local process settings, not the exposure list or pairing data.
type Config struct {
	AdapterID              string         `yaml:"adapter_id"`
	NATSURL                *string        `yaml:"nats_url"`
	CoreURL                string         `yaml:"core_url"`
	DataDir                string         `yaml:"data_dir"`
	BridgeName             *string        `yaml:"bridge_name"`
	ListenPort             *int           `yaml:"listen_port"`
	Interfaces             []string       `yaml:"interfaces"`
	ResyncInterval         *time.Duration `yaml:"resync_interval"`
	TemperatureDisplayUnit *string        `yaml:"temperature_display_unit"`
}

// Resolve fills omitted settings and validates the effective configuration.
func (c Config) Resolve() (Config, error) {
	c.NATSURL = defaultValue(c.NATSURL, "nats://127.0.0.1:4222")
	c.BridgeName = defaultValue(c.BridgeName, "Hearth")
	c.ListenPort = defaultValue(c.ListenPort, defaultListenPort)
	c.ResyncInterval = defaultValue(c.ResyncInterval, time.Minute)
	c.TemperatureDisplayUnit = defaultValue(c.TemperatureDisplayUnit, "celsius")
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) validate() error {
	const minPort, maxPort = 1024, 65535
	const maxName = 64
	const minResync, maxResync = 15 * time.Second, 10 * time.Minute

	if err := bridge.ValidateCoreURL(c.CoreURL); err != nil {
		return err
	}
	switch {
	case !filepath.IsAbs(c.DataDir):
		return errors.New("data_dir must be an absolute path")
	case strings.TrimSpace(*c.BridgeName) == "" || utf8.RuneCountInString(*c.BridgeName) > maxName:
		return errors.New("bridge_name must contain 1-64 characters and cannot be blank")
	case *c.ListenPort < minPort || *c.ListenPort > maxPort:
		return errors.New("listen_port must be between 1024 and 65535")
	case *c.ResyncInterval < minResync || *c.ResyncInterval > maxResync:
		return errors.New("resync_interval must be between 15s and 10m")
	case *c.TemperatureDisplayUnit != "celsius" && *c.TemperatureDisplayUnit != "fahrenheit":
		return errors.New("temperature_display_unit must be celsius or fahrenheit")
	}
	if err := platformconfig.ValidateSlug("adapter_id", c.AdapterID); err != nil {
		return err
	}
	return platformconfig.ValidateNATSURL(*c.NATSURL)
}

func defaultValue[T any](value *T, fallback T) *T {
	if value != nil {
		return value
	}
	return new(fallback)
}
