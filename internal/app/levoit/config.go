// Package levoit assembles the Levoit companion process.
package levoit

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	purifier "github.com/mholtzscher/hearth/internal/adapters/levoit"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

type Config struct {
	AdapterID       string         `yaml:"adapter_id"`
	NATSURL         string         `yaml:"nats_url"`
	CredentialsFile string         `yaml:"credentials_file"`
	PollInterval    *time.Duration `yaml:"poll_interval"`
	TimeZone        string         `yaml:"time_zone"`
}

func (c Config) Validate() error {
	if err := platformconfig.ValidateSlug("adapter_id", c.AdapterID); err != nil {
		return err
	}
	if err := platformconfig.ValidateNATSURL(c.NATSURL); err != nil {
		return err
	}
	if strings.TrimSpace(c.CredentialsFile) == "" {
		return errors.New("credentials_file is required")
	}
	_, err := c.runtimeConfig()
	return err
}

func (c Config) runtimeConfig() (purifier.Config, error) {
	config := purifier.Config{PollInterval: time.Minute, TimeZone: "UTC"}
	if c.PollInterval != nil {
		config.PollInterval = *c.PollInterval
	}
	if c.TimeZone != "" {
		config.TimeZone = c.TimeZone
	}
	return config, config.Validate()
}

func readCredentials(path string) (purifier.Credentials, error) {
	const maximumBytes = 16 * 1024
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return purifier.Credentials{}, errors.New("credentials must reference a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return purifier.Credentials{}, errors.New("credentials file could not be read")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return purifier.Credentials{}, errors.New("credentials must reference a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil || len(data) > maximumBytes {
		return purifier.Credentials{}, errors.New("credentials file is unreadable or oversized")
	}
	var credentials purifier.Credentials
	if err = json.Unmarshal(data, &credentials); err != nil {
		return purifier.Credentials{}, errors.New("credentials must be a JSON object with email and password")
	}
	if err = credentials.Validate(); err != nil {
		return purifier.Credentials{}, err
	}
	return credentials, nil
}
