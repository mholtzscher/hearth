package hearthd

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Embed household timezone rules instead of requiring host zoneinfo.

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

const (
	// DefaultObservationRetention bounds how long Core keeps non-current observations.
	DefaultObservationRetention = 30 * 24 * time.Hour
	// MinimumObservationRetention keeps the Core window above the seven-day JetStream retention.
	MinimumObservationRetention = devices.MinimumObservationRetention
	// DefaultAutomationHistoryRetention bounds how long Core keeps terminal Automation history.
	DefaultAutomationHistoryRetention = 30 * 24 * time.Hour
	// MinimumAutomationHistoryRetention keeps pruned Automation history above the
	// seven-day Device Fact retention so retained evidence stays explainable.
	MinimumAutomationHistoryRetention = automations.MinimumAutomationHistoryRetention
	// AutomationFactMaximumAge is the fixed semantic freshness bound for one
	// Device Fact. It is deliberately not operator configuration.
	AutomationFactMaximumAge = automations.AutomationFactMaximumAge
)

type Config struct {
	HouseholdTimezone string `yaml:"household_timezone"`
	HTTPAddr          string `yaml:"http_addr"`
	NATSURL           string `yaml:"nats_url"`
	SQLitePath        string `yaml:"sqlite_path"`
	// ObservationRetention bounds how long Core keeps non-current observations.
	// Zero selects DefaultObservationRetention; a restart applies policy changes
	// on the startup prune pass.
	ObservationRetention time.Duration `yaml:"observation_retention"`
	// AutomationHistoryRetention bounds how long Core keeps terminal Automation
	// Runs and Skips. Zero selects DefaultAutomationHistoryRetention; a restart
	// applies policy changes on the startup prune pass.
	AutomationHistoryRetention time.Duration `yaml:"automation_history_retention"`
}

func LoadConfig(path string) (Config, error) {
	var value Config
	if err := platformconfig.LoadFile(path, &value); err != nil {
		return Config{}, err
	}
	if value.ObservationRetention == 0 {
		value.ObservationRetention = DefaultObservationRetention
	}
	if value.AutomationHistoryRetention == 0 {
		value.AutomationHistoryRetention = DefaultAutomationHistoryRetention
	}
	if err := value.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config %q: %w", path, err)
	}
	return value, nil
}

// EffectiveObservationRetention returns the configured observation retention,
// or DefaultObservationRetention when the setting is unset.
func (value Config) EffectiveObservationRetention() time.Duration {
	if value.ObservationRetention == 0 {
		return DefaultObservationRetention
	}
	return value.ObservationRetention
}

// EffectiveAutomationHistoryRetention returns the configured Automation history
// retention, or DefaultAutomationHistoryRetention when the setting is unset.
func (value Config) EffectiveAutomationHistoryRetention() time.Duration {
	if value.AutomationHistoryRetention == 0 {
		return DefaultAutomationHistoryRetention
	}
	return value.AutomationHistoryRetention
}

// LoadHouseholdTimezone loads an IANA location from embedded timezone data.
func (value Config) LoadHouseholdTimezone() (*time.Location, error) {
	name := value.HouseholdTimezone
	if name == "" || name == "Local" || strings.TrimSpace(name) != name || strings.HasPrefix(name, "+") ||
		strings.HasPrefix(name, "-") {
		return nil, fmt.Errorf("household_timezone must be an IANA timezone")
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("household_timezone must be an IANA timezone")
	}
	return location, nil
}

func (value Config) Validate() error {
	_, err := value.validateAndLoadHouseholdTimezone()
	return err
}

// validateAndLoadHouseholdTimezone lets Run validate and retain one loaded location.
func (value Config) validateAndLoadHouseholdTimezone() (*time.Location, error) {
	location, err := value.LoadHouseholdTimezone()
	if err != nil {
		return nil, err
	}
	if err = value.validateRuntimeSettings(); err != nil {
		return nil, err
	}
	return location, nil
}

func (value Config) validateRuntimeSettings() error {
	_, portText, err := net.SplitHostPort(value.HTTPAddr)
	if err != nil {
		return fmt.Errorf("http_addr must contain a host and port: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("http_addr port must be between 1 and 65535")
	}
	if validationErr := platformconfig.ValidateNATSURL(value.NATSURL); validationErr != nil {
		return validationErr
	}
	if strings.TrimSpace(value.SQLitePath) == "" {
		return fmt.Errorf("sqlite_path is required")
	}
	if value.ObservationRetention != 0 && value.ObservationRetention < MinimumObservationRetention {
		return fmt.Errorf(
			"observation_retention must be at least %s", MinimumObservationRetention,
		)
	}
	if value.AutomationHistoryRetention != 0 &&
		value.AutomationHistoryRetention < MinimumAutomationHistoryRetention {
		return fmt.Errorf(
			"automation_history_retention must be at least %s", MinimumAutomationHistoryRetention,
		)
	}
	return nil
}
