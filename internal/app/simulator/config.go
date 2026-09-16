package simulator

import (
	"fmt"
	"net"
	"strings"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	simulatoradapter "github.com/mholtzscher/hearth/internal/adapters/simulator"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

type Config struct {
	AdapterID  string `yaml:"adapter_id"`
	NATSURL    string `yaml:"nats_url"`
	BindingKey string `yaml:"binding_key"`
	Scenario   string `yaml:"scenario"`
	// ControlAddr optionally enables the loopback control channel
	// (for example "127.0.0.1:8181"). Absent disables it.
	ControlAddr string                `yaml:"control_addr"`
	Devices     []scripted.DeviceSpec `yaml:"devices"`
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
	if strings.TrimSpace(value.ControlAddr) != "" {
		if err := validateLoopbackAddr(value.ControlAddr); err != nil {
			return err
		}
	}
	scenarioSet := strings.TrimSpace(value.Scenario) != ""
	devicesSet := len(value.Devices) > 0
	switch {
	case scenarioSet && devicesSet:
		return fmt.Errorf("scenario and devices are mutually exclusive")
	case !scenarioSet && !devicesSet:
		return fmt.Errorf("one of scenario or devices is required")
	case scenarioSet:
		return value.validateScenario()
	default:
		return value.validateScripted()
	}
}

func (value Config) validateScenario() error {
	if err := platformconfig.ValidateSlug("binding_key", value.BindingKey); err != nil {
		return err
	}
	if !simulatoradapter.ValidScenario(value.Scenario) {
		return fmt.Errorf("unknown scenario %q", value.Scenario)
	}
	return nil
}

func (value Config) validateScripted() error {
	if strings.TrimSpace(value.BindingKey) != "" {
		return fmt.Errorf("binding_key is legacy scenario-only; scripted Devices carry their own binding keys")
	}
	for index := range value.Devices {
		if err := value.Devices[index].Validate(); err != nil {
			return fmt.Errorf("devices[%d]: %w", index, err)
		}
	}
	// Structural checks pass above, but value schemas are only normalized
	// against the Entity type registry by the scripted runtime. Validate them
	// here too so a bad value fails at config load, before NATS is connected.
	if err := scripted.ValidateValues(value.Devices); err != nil {
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
