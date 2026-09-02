package zigbee2mqtt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

type Config struct {
	AdapterID string     `yaml:"adapter_id"`
	NATSURL   string     `yaml:"nats_url"`
	MQTT      MQTTConfig `yaml:"mqtt"`
}

type MQTTConfig struct {
	URL       string `yaml:"url"`
	BaseTopic string `yaml:"base_topic"`
}

func LoadConfig(path string) (Config, error) {
	var value Config
	if err := platformconfig.LoadFile(path, &value); err != nil {
		return Config{}, err
	}
	if err := value.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config %q: %w", path, err)
	}
	value.MQTT.URL = normalizeMQTTURL(value.MQTT.URL)
	return value, nil
}

func (value Config) Validate() error {
	if err := platformconfig.ValidateSlug("adapter_id", value.AdapterID); err != nil {
		return err
	}
	if err := platformconfig.ValidateNATSURL(value.NATSURL); err != nil {
		return err
	}
	if err := validateMQTTURL(value.MQTT.URL); err != nil {
		return err
	}
	if err := platformconfig.ValidateSlug("mqtt.base_topic", value.MQTT.BaseTopic); err != nil {
		return err
	}
	return nil
}

func DeriveClientID(adapterID string) string {
	digest := sha256.Sum256([]byte(adapterID))
	return "hearth-z2m-" + hex.EncodeToString(digest[:6])
}

func validateMQTTURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "mqtt" && parsed.Scheme != "tcp") ||
		parsed.Hostname() == "" || parsed.Port() == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || strings.Contains(value, "#") {
		return fmt.Errorf(
			"mqtt.url must be an absolute mqtt:// or tcp:// URL with an explicit host and port " +
				"and no user info, path, query, or fragment",
		)
	}
	port, err := strconv.ParseUint(parsed.Port(), 10, 16)
	if err != nil || port == 0 {
		return fmt.Errorf("mqtt.url must contain a valid TCP port")
	}
	return nil
}

func normalizeMQTTURL(value string) string {
	if remainder, found := strings.CutPrefix(value, "mqtt://"); found {
		return "tcp://" + remainder
	}
	return value
}
