package simulator

import (
	"fmt"
	"strings"

	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

type Config struct {
	AdapterID  string `yaml:"adapter_id"`
	NATSURL    string `yaml:"nats_url"`
	BindingKey string `yaml:"binding_key"`
	Scenario   string `yaml:"scenario"`
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
	if err := platformconfig.ValidateSlug("binding_key", value.BindingKey); err != nil {
		return err
	}
	if strings.TrimSpace(value.Scenario) == "" {
		return fmt.Errorf("scenario is required")
	}
	return nil
}
