package simulator

import (
	"fmt"
	"net"
	"strings"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
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

func LoadConfig(path string) (Config, error) {
	var value Config
	if err := platformconfig.LoadFile(path, &value); err != nil {
		return Config{}, err
	}
	if err := value.Validate(); err != nil {
		return Config{}, platformconfig.Invalid(path, err)
	}
	return value, nil
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
