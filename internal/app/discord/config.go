// Package discord assembles the session-backed hearth-adapter-discord notification process.
package discord

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

type DestinationConfig struct {
	Name           string `yaml:"name"`
	WebhookURLFile string `yaml:"webhook_url_file"`
}

type Config struct {
	AdapterID    string                       `yaml:"adapter_id"`
	NATSURL      string                       `yaml:"nats_url"`
	Destinations map[string]DestinationConfig `yaml:"destinations"`
}

const (
	maximumSecretBytes = 4096
	// maximumDestinations matches the function schema enum list bound.
	maximumDestinations = 64
)

func (config Config) Validate() error {
	if err := platformconfig.ValidateSlug("adapter_id", config.AdapterID); err != nil {
		return err
	}
	if err := platformconfig.ValidateNATSURL(config.NATSURL); err != nil {
		return err
	}
	if len(config.Destinations) == 0 {
		return errors.New("at least one destination is required")
	}
	if len(config.Destinations) > maximumDestinations {
		return fmt.Errorf("at most %d destinations are supported", maximumDestinations)
	}
	for key, destination := range config.Destinations {
		if err := platformconfig.ValidateSlug("destination key", key); err != nil {
			return err
		}
		name := strings.TrimSpace(destination.Name)
		if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > 200 {
			return errors.New("destination name must contain 1-200 characters after trimming")
		}
		if strings.TrimSpace(destination.WebhookURLFile) == "" {
			return errors.New("destination webhook_url_file is required")
		}
	}
	return nil
}

func loadCredentials(config Config) (map[string]string, error) {
	routes := make(map[string]string, len(config.Destinations))
	for key, destination := range config.Destinations {
		secret, err := readSecret(destination.WebhookURLFile)
		if err != nil {
			return nil, err
		}
		routes[key] = secret
	}
	return routes, nil
}

func readSecret(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("webhook secret must reference a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("webhook secret file could not be read")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("webhook secret must reference a regular file")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumSecretBytes+1))
	if err != nil || len(contents) > maximumSecretBytes {
		return "", errors.New("webhook secret file is unreadable or oversized")
	}
	return strings.TrimSpace(string(contents)), nil
}
