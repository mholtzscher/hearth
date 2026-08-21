package homeassistant

import (
	"fmt"
	"net/url"
	"strings"

	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

type Config struct {
	AdapterID string         `yaml:"adapter_id"`
	NATSURL   string         `yaml:"nats_url"`
	Binding   BindingConfig  `yaml:"binding"`
	Upstream  UpstreamConfig `yaml:"home_assistant"`
}

type BindingConfig struct {
	Key              string  `yaml:"key"`
	DeviceExternalID *string `yaml:"device_external_id,omitempty"`
	DeviceName       string  `yaml:"device_name"`
	EntityExternalID string  `yaml:"entity_id"`
	EntityName       string  `yaml:"entity_name"`
}

type UpstreamConfig struct {
	URL       string `yaml:"url"`
	TokenFile string `yaml:"token_file"`
}

func LoadConfig(path string) (Config, error) {
	var value Config
	if err := platformconfig.LoadFile(path, &value); err != nil {
		return Config{}, err
	}
	if err := value.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config %q: %w", path, err)
	}
	return value, nil
}

func (value Config) Validate() error {
	if err := platformconfig.ValidateSlug("adapter_id", value.AdapterID); err != nil {
		return err
	}
	if err := platformconfig.ValidateNATSURL(value.NATSURL); err != nil {
		return err
	}
	if err := platformconfig.ValidateSlug("binding.key", value.Binding.Key); err != nil {
		return err
	}
	if err := platformconfig.ValidateName("binding.device_name", value.Binding.DeviceName); err != nil {
		return err
	}
	if value.Binding.DeviceExternalID != nil {
		if err := platformconfig.ValidateExternalID("binding.device_external_id", *value.Binding.DeviceExternalID); err != nil {
			return err
		}
	}
	if err := platformconfig.ValidateExternalID("binding.entity_id", value.Binding.EntityExternalID); err != nil {
		return err
	}
	if err := platformconfig.ValidateName("binding.entity_name", value.Binding.EntityName); err != nil {
		return err
	}
	parsed, err := url.Parse(value.Upstream.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "ws" && parsed.Scheme != "wss") {
		return fmt.Errorf("home_assistant.url must be an absolute HTTP or WebSocket URL")
	}
	if strings.TrimSpace(value.Upstream.TokenFile) == "" {
		return fmt.Errorf("home_assistant.token_file is required")
	}
	return nil
}
