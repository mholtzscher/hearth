// Package pirateweather assembles the Pirate Weather companion process.
package pirateweather

import (
	"errors"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	weather "github.com/mholtzscher/hearth/internal/adapters/pirateweather"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

type LocationConfig struct {
	Name      string   `yaml:"name"`
	Latitude  *float64 `yaml:"latitude"`
	Longitude *float64 `yaml:"longitude"`
}

type Config struct {
	AdapterID    string         `yaml:"adapter_id"`
	NATSURL      string         `yaml:"nats_url"`
	APIKeyFile   string         `yaml:"api_key_file"`
	Location     LocationConfig `yaml:"location"`
	PollInterval *time.Duration `yaml:"poll_interval"`
}

const (
	maximumSecretBytes  = 4096
	defaultPollInterval = 15 * time.Minute
)

func (c Config) Validate() error {
	if err := platformconfig.ValidateSlug("adapter_id", c.AdapterID); err != nil {
		return err
	}
	if err := platformconfig.ValidateNATSURL(c.NATSURL); err != nil {
		return err
	}
	if strings.TrimSpace(c.APIKeyFile) == "" {
		return errors.New("api_key_file is required")
	}
	_, err := c.runtimeConfig()
	return err
}

func (c Config) runtimeConfig() (weather.Config, error) {
	if c.Location.Latitude == nil || c.Location.Longitude == nil {
		return weather.Config{}, errors.New("latitude and longitude are required")
	}
	interval := defaultPollInterval
	if c.PollInterval != nil {
		interval = *c.PollInterval
	}
	config := weather.Config{LocationName: strings.TrimSpace(c.Location.Name), Latitude: *c.Location.Latitude,
		Longitude: *c.Location.Longitude, PollInterval: interval}
	return config, config.Validate()
}

func readSecret(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("API key must reference a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("API key file could not be read")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("API key must reference a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumSecretBytes+1))
	if err != nil || len(data) > maximumSecretBytes {
		return "", errors.New("API key file is unreadable or oversized")
	}
	key := strings.TrimSpace(string(data))
	if key == "" || !utf8.ValidString(key) || strings.ContainsAny(key, "\r\n") {
		return "", errors.New("API key must be nonempty single-line text")
	}
	return key, nil
}
