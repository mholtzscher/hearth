// Package ecowitt assembles the hearth-adapter-ecowitt process: it loads and
// validates operator configuration together with the station PASSKEY, derives
// the deterministic MQTT client ID, and supervises the Ecowitt Adapter and its
// SDK Session under one lifecycle.
package ecowitt

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

// Client ID prefix and hashed suffix length. The derived client ID is exactly
// 23 characters, the MQTT 3.1.1 client-identifier compatibility bound.
const (
	clientIDPrefix            = "hearth-eco-"
	clientIDHashHexDigits     = 12
	uploadIntervalSecondsLow  = 8
	uploadIntervalSecondsHigh = 600
	passkeyHexDigits          = 32
	maximumDeviceNameRunes    = 128
)

// exactTopicPattern is the two-segment subject-safe topic shape. The second
// segment is commonly a station MAC, so the configured topic never appears in
// an error or a log.
var exactTopicPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}/[a-z0-9][a-z0-9_-]{0,62}$`)

// Config is the operator configuration for one Ecowitt Adapter instance. It is
// never logged: it carries the configured MQTT topic and the PASSKEY-file
// path, both of which are station-identifying.
type Config struct {
	AdapterID string        `yaml:"adapter_id"`
	NATSURL   string        `yaml:"nats_url"`
	MQTT      MQTTConfig    `yaml:"mqtt"`
	Station   StationConfig `yaml:"station"`
}

// MQTTConfig is the plain, unauthenticated MQTT endpoint plus the one exact
// subscription topic.
type MQTTConfig struct {
	URL   string `yaml:"url"`
	Topic string `yaml:"topic"`
}

// StationConfig is the configured station: the two slot Device display names,
// the PASSKEY-file path, and the gateway upload cadence that scales every
// freshness deadline.
type StationConfig struct {
	GatewayName           string `yaml:"gateway_name"`
	OutdoorArrayName      string `yaml:"outdoor_array_name"`
	PasskeyFile           string `yaml:"passkey_file"`
	UploadIntervalSeconds int    `yaml:"upload_interval_seconds"`
}

// LoadConfig strictly decodes one Ecowitt YAML file, validates every A3
// constraint, and normalizes mqtt:// to Paho's tcp://. It does not read the
// PASSKEY itself: Run loads the secret at process start so a static file that
// only describes the path stays free of secret material.
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

// Validate enforces the complete static configuration contract. Every failure
// names its field and never repeats the PASSKEY, the configured topic, or the
// secret-file contents.
func (value Config) Validate() error {
	if err := platformconfig.ValidateSlug("adapter_id", value.AdapterID); err != nil {
		return err
	}
	if err := platformconfig.ValidateNATSURL(value.NATSURL); err != nil {
		return err
	}
	if err := validateMQTTBrokerURL(value.MQTT.URL); err != nil {
		return err
	}
	if err := validateMQTTTopic(value.MQTT.Topic); err != nil {
		return err
	}
	if err := validateDeviceName("station.gateway_name", value.Station.GatewayName); err != nil {
		return err
	}
	if err := validateDeviceName("station.outdoor_array_name", value.Station.OutdoorArrayName); err != nil {
		return err
	}
	if _, err := LoadPasskeyFile(value.Station.PasskeyFile); err != nil {
		return err
	}
	if value.Station.UploadIntervalSeconds < uploadIntervalSecondsLow ||
		value.Station.UploadIntervalSeconds > uploadIntervalSecondsHigh {
		return fmt.Errorf(
			"station.upload_interval_seconds must be between %d and %d",
			uploadIntervalSecondsLow, uploadIntervalSecondsHigh,
		)
	}
	return nil
}

// DeriveClientID derives the deterministic 23-character MQTT client ID:
// "hearth-eco-" plus the first twelve lowercase hexadecimal characters of
// SHA-256(adapter_id). The client ID aids broker diagnosis only; a clean
// session creates no durable Adapter state.
func DeriveClientID(adapterID string) string {
	digest := sha256.Sum256([]byte(adapterID))
	return clientIDPrefix + hex.EncodeToString(digest[:clientIDHashHexDigits/2])
}

// LoadPasskeyFile reads the configured PASSKEY file and requires exactly one
// trimmed 32-character hexadecimal PASSKEY. Errors are fixed classifications:
// they never repeat the file path, the file contents, or the decoded secret.
func LoadPasskeyFile(path string) ([16]byte, error) {
	var passkey [16]byte
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return passkey, errors.New("station.passkey_file is required")
	}
	info, err := os.Stat(trimmed)
	if err != nil || !info.Mode().IsRegular() {
		return passkey, errors.New("station.passkey_file must reference a regular file")
	}
	contents, err := os.ReadFile(trimmed)
	if err != nil {
		return passkey, errors.New("station.passkey_file could not be read")
	}
	secret := strings.TrimSpace(string(contents))
	if len(secret) != passkeyHexDigits {
		return passkey, errors.New("station.passkey_file must contain one 32-character hexadecimal PASSKEY")
	}
	decoded, err := hex.DecodeString(secret)
	if err != nil {
		return passkey, errors.New("station.passkey_file must contain one 32-character hexadecimal PASSKEY")
	}
	copy(passkey[:], decoded)
	return passkey, nil
}

// validateMQTTBrokerURL requires an absolute plain lowercase mqtt:// or tcp://
// URL with an explicit host and port and no user info, path, query, fragment,
// or TLS scheme. It never returns the URL in an error.
func validateMQTTBrokerURL(value string) error {
	// Match the exact lowercase prefix before parsing: url.Parse lowercases
	// parsed.Scheme, so a case-variant scheme like MQTT:// or TCP:// would
	// otherwise pass here and only fail later in the MQTT transport.
	lowercaseScheme := strings.HasPrefix(value, "mqtt://") || strings.HasPrefix(value, "tcp://")
	parsed, err := url.Parse(value)
	if err != nil || !lowercaseScheme || (parsed.Scheme != "mqtt" && parsed.Scheme != "tcp") ||
		parsed.Hostname() == "" || parsed.Port() == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || strings.Contains(value, "#") {
		return errors.New(
			"mqtt.url must be an absolute mqtt:// or tcp:// URL with an explicit host and port " +
				"and no user info, path, query, or fragment",
		)
	}
	port, err := strconv.ParseUint(parsed.Port(), 10, 16)
	if err != nil || port == 0 {
		return errors.New("mqtt.url must contain a valid TCP port")
	}
	return nil
}

// validateMQTTTopic requires one exact two-segment subject-safe topic and
// rejects every MQTT wildcard.
func validateMQTTTopic(topic string) error {
	if strings.ContainsAny(topic, "#+") || !exactTopicPattern.MatchString(topic) {
		return errors.New(
			"mqtt.topic must be exactly two lowercase subject-safe segments without MQTT wildcards",
		)
	}
	return nil
}

// validateDeviceName requires 1 to 128 runes after trimming.
func validateDeviceName(field, name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > maximumDeviceNameRunes {
		return fmt.Errorf("%s must contain 1 to %d characters after trimming", field, maximumDeviceNameRunes)
	}
	return nil
}

// normalizeMQTTURL maps the operator's mqtt:// alias to Paho's tcp:// scheme.
// Any other scheme is already normalized or rejected by validation.
func normalizeMQTTURL(value string) string {
	if remainder, found := strings.CutPrefix(value, "mqtt://"); found {
		return "tcp://" + remainder
	}
	return value
}
