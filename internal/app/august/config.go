// Package august assembles the August monitoring companion.
package august

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	provider "github.com/mholtzscher/hearth/internal/adapters/august"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

type Config struct {
	AdapterID         string         `yaml:"adapter_id"`
	NATSURL           string         `yaml:"nats_url"`
	CredentialsFile   string         `yaml:"credentials_file"`
	InstallationID    string         `yaml:"installation_id"`
	PollInterval      *time.Duration `yaml:"poll_interval"`
	InventoryInterval *time.Duration `yaml:"inventory_interval"`
	Locks             []LockConfig   `yaml:"locks"`
}

type LockConfig struct {
	BindingKey      string `yaml:"binding_key"`
	LockID          string `yaml:"lock_id"`
	Name            string `yaml:"name"`
	CommandsEnabled bool   `yaml:"commands_enabled"`
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

func (c Config) runtimeConfig() (provider.Config, error) {
	const defaultPoll = 5 * time.Minute
	const defaultInventory = 15 * time.Minute
	result := provider.Config{
		InstallationID:    c.InstallationID,
		PollInterval:      defaultPoll,
		InventoryInterval: defaultInventory,
	}
	if c.PollInterval != nil {
		result.PollInterval = *c.PollInterval
	}
	if c.InventoryInterval != nil {
		result.InventoryInterval = *c.InventoryInterval
	}
	for _, lock := range c.Locks {
		result.Bindings = append(
			result.Bindings,
			provider.BindingConfig{
				BindingKey:      lock.BindingKey,
				LockID:          strings.ToUpper(lock.LockID),
				Name:            lock.Name,
				CommandsEnabled: lock.CommandsEnabled,
			},
		)
	}
	return result, result.Validate()
}

func readCredentials(path string) (provider.Credentials, error) {
	const maximumBytes = 16 * 1024
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return provider.Credentials{}, errors.New("credentials must reference a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return provider.Credentials{}, errors.New("credentials file could not be read")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return provider.Credentials{}, errors.New("credentials must be a private regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil || len(data) > maximumBytes {
		return provider.Credentials{}, errors.New("credentials file is unreadable or oversized")
	}
	var credentials provider.Credentials
	if err = json.Unmarshal(data, &credentials); err != nil {
		return provider.Credentials{}, errors.New("credentials must be a JSON object with email and password")
	}
	if err = credentials.Validate(); err != nil {
		return provider.Credentials{}, err
	}
	return credentials, nil
}
