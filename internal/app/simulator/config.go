package simulator

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"

	"gopkg.in/yaml.v3"
)

type Config struct {
	AdapterID string `yaml:"adapter_id"`
	NATSURL   string `yaml:"nats_url"`
	// ControlAddr optionally enables the loopback control channel
	// (for example "127.0.0.1:8181"). Absent disables it.
	ControlAddr string                  `yaml:"control_addr"`
	Devices     []scripted.DeviceSpec   `yaml:"devices"`
	Adapters    []ScriptedAdapterConfig `yaml:"adapters"`
}

// ScriptedAdapterConfig defines the Devices owned by one simulated Adapter.
type ScriptedAdapterConfig struct {
	AdapterID string                `yaml:"adapter_id"`
	Devices   []scripted.DeviceSpec `yaml:"devices"`
}

func (value Config) scriptedAdapters() []ScriptedAdapterConfig {
	if len(value.Adapters) != 0 {
		return value.Adapters
	}
	return []ScriptedAdapterConfig{{AdapterID: value.AdapterID, Devices: value.Devices}}
}

// ConfigOverrides replaces scalar simulator settings before validation.
// Unset empty fields leave their YAML values unchanged.
type ConfigOverrides struct {
	AdapterID      string
	NATSURL        string
	ControlAddr    string
	AdapterIDSet   bool
	NATSURLSet     bool
	ControlAddrSet bool
}

func LoadConfig(path string) (Config, error) {
	return LoadConfigWithOverrides(path, ConfigOverrides{})
}

// LoadConfigWithOverrides loads a required simulator YAML file and applies overrides.
func LoadConfigWithOverrides(path string, overrides ConfigOverrides) (Config, error) {
	return LoadConfigFromSources(path, true, overrides)
}

// LoadConfigFromSources loads optional implicit YAML, applies explicitly set
// scalar values, then validates the resulting simulator configuration.
func LoadConfigFromSources(path string, explicit bool, overrides ConfigOverrides) (Config, error) {
	var value Config
	if err := loadYAML(path, explicit, &value); err != nil {
		return Config{}, err
	}
	if overrides.AdapterIDSet || overrides.AdapterID != "" {
		value.AdapterID = overrides.AdapterID
	}
	if overrides.NATSURLSet || overrides.NATSURL != "" {
		value.NATSURL = overrides.NATSURL
	}
	if overrides.ControlAddrSet || overrides.ControlAddr != "" {
		value.ControlAddr = overrides.ControlAddr
	}
	if err := value.Validate(); err != nil {
		return Config{}, platformconfig.Invalid(path, err)
	}
	return value, nil
}

// loadYAML intentionally does not enable KnownFields: simulator config is
// extensible and unknown YAML fields are ignored, but syntax and documents are checked.
func loadYAML(path string, explicit bool, destination *Config) error {
	file, err := os.Open(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return errors.New("configuration file could not be read")
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decodeErr := decoder.Decode(destination)
	if decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		return errors.New("configuration file contains invalid YAML")
	}
	var extra any
	decodeErr = decoder.Decode(&extra)
	if !errors.Is(decodeErr, io.EOF) {
		return errors.New("configuration file must contain a single YAML document")
	}
	return nil
}

func (value Config) Validate() error {
	if len(value.Adapters) != 0 && (value.AdapterID != "" || value.Devices != nil) {
		return fmt.Errorf("adapters cannot be combined with adapter_id or devices")
	}
	if err := platformconfig.ValidateNATSURL(value.NATSURL); err != nil {
		return err
	}
	if strings.TrimSpace(value.ControlAddr) != "" {
		if err := validateLoopbackAddr(value.ControlAddr); err != nil {
			return err
		}
	}
	seen := make(map[string]bool)
	for index, entry := range value.scriptedAdapters() {
		if err := platformconfig.ValidateSlug("adapter_id", entry.AdapterID); err != nil {
			return fmt.Errorf("adapters[%d]: %w", index, err)
		}
		if seen[entry.AdapterID] {
			return fmt.Errorf("adapters[%d]: duplicate adapter_id %q", index, entry.AdapterID)
		}
		seen[entry.AdapterID] = true
		if err := validateScriptedDevices(entry.Devices); err != nil {
			return fmt.Errorf("adapter %q: %w", entry.AdapterID, err)
		}
	}
	return nil
}

// validateScripted requires at least one scripted Device and validates every
// Device's structure and value schemas. Value schemas are only normalized
// against the Entity type registry by the scripted runtime, so validating them
// here makes a bad value fail at config load, before NATS is connected.
func validateScriptedDevices(devices []scripted.DeviceSpec) error {
	for index := range devices {
		if err := devices[index].Validate(); err != nil {
			return fmt.Errorf("devices[%d]: %w", index, err)
		}
	}
	if err := scripted.ValidateValues(devices); err != nil {
		return err
	}
	return nil
}

// validateLoopbackAddr keeps the agent control channel off the network: only
// loopback hosts with an explicit port are accepted.
func validateLoopbackAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("control_addr must be host:port: %w", err)
	}
	if port == "" || port == "0" {
		return fmt.Errorf("control_addr requires an explicit port")
	}
	if host == "localhost" {
		return nil
	}
	parsed := net.ParseIP(host)
	if parsed == nil || !parsed.IsLoopback() {
		return fmt.Errorf("control_addr must be loopback, got %q", host)
	}
	return nil
}
