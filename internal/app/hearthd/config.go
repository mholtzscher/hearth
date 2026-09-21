package hearthd

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Embed household timezone rules instead of requiring host zoneinfo.

	"github.com/cloudwego/eino/components/model"

	"github.com/mholtzscher/hearth/internal/modules/agent"
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
	AutomationFactMaximumAge = automations.FactMaximumAge
	// DefaultAgentModel is the household chat model the agent uses unless
	// configured otherwise.
	DefaultAgentModel = "gpt-5.6-luna"
	// DefaultAgentReasoningEffort is the reasoning level Core selects for
	// DefaultAgentModel when agent.reasoning_effort is unset: that model rejects
	// function tools at any other level, and a turn without its tool catalog
	// cannot do household work.
	DefaultAgentReasoningEffort = "none"
	// DefaultAgentHistoryRetention bounds how long Core keeps agent conversations.
	DefaultAgentHistoryRetention = 30 * 24 * time.Hour
	// MinimumAgentHistoryRetention keeps agent conversation retention at the
	// module's own floor: a shorter window would delete more than intended.
	MinimumAgentHistoryRetention = agent.MinimumConversationRetention
)

// agentReasoningEfforts lists the model reasoning levels Core forwards to the
// provider verbatim; an empty setting selects the provider's own default. It is
// a function, not package state, so the closed list has one owner.
func agentReasoningEfforts() []string {
	return []string{"none", "minimal", "low", "medium", "high"}
}

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
	// Agent is the required household agent configuration. Core always
	// constructs the agent, so the block and its API key file are required.
	Agent AgentConfig `yaml:"agent"`
}

// AgentConfig configures the required household agent. The secret lives in a
// separate local file, and every other value is ordinary non-secret
// configuration.
type AgentConfig struct {
	// APIKeyFile is the local secret file holding the model API key. Core
	// requires it, never logs its path or contents, and never persists the key.
	APIKeyFile string `yaml:"api_key_file"`
	// Model names the chat model. Empty selects DefaultAgentModel; a restart
	// applies a change to later turns.
	Model string `yaml:"model"`
	// BaseURL overrides the model provider endpoint. Empty uses the provider's
	// own default; a set value must be an absolute http or https URL.
	BaseURL string `yaml:"base_url"`
	// ReasoningEffort selects the model's reasoning level from
	// agentReasoningEfforts. Empty selects DefaultAgentReasoningEffort for
	// DefaultAgentModel and the provider's own default for any other model.
	ReasoningEffort string `yaml:"reasoning_effort"`
	// MaxSteps bounds one turn's model plus tools steps. Zero selects the agent
	// module's own default.
	MaxSteps int `yaml:"max_steps"`
	// HistoryRetention bounds how long Core keeps whole agent conversations.
	// Zero selects DefaultAgentHistoryRetention; a restart applies policy
	// changes on the startup prune pass.
	HistoryRetention time.Duration `yaml:"history_retention"`
	// ChatModel replaces the provider chat model the agent would construct,
	// which lets tests and embeddings run a turn without model credentials. It
	// is deliberately not YAML configuration.
	ChatModel model.ToolCallingChatModel `yaml:"-"`
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
	if value.Agent.Model == "" {
		value.Agent.Model = DefaultAgentModel
	}
	if value.Agent.ReasoningEffort == "" && value.Agent.Model == DefaultAgentModel {
		value.Agent.ReasoningEffort = DefaultAgentReasoningEffort
	}
	if value.Agent.HistoryRetention == 0 {
		value.Agent.HistoryRetention = DefaultAgentHistoryRetention
	}
	if err := value.Validate(); err != nil {
		return Config{}, platformconfig.Invalid(path, err)
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

// EffectiveAgentModel returns the configured agent model, or
// DefaultAgentModel when the setting is unset.
func (value Config) EffectiveAgentModel() string {
	if value.Agent.Model == "" {
		return DefaultAgentModel
	}
	return value.Agent.Model
}

// EffectiveAgentHistoryRetention returns the configured agent conversation
// retention, or DefaultAgentHistoryRetention when the setting is unset.
func (value Config) EffectiveAgentHistoryRetention() time.Duration {
	if value.Agent.HistoryRetention == 0 {
		return DefaultAgentHistoryRetention
	}
	return value.Agent.HistoryRetention
}

// LoadAgentAPIKey reads the model API key from the required agent secret file.
// Its errors classify the failure without repeating the configured path or the
// file's contents, so a process record can publish them safely.
func (value Config) LoadAgentAPIKey() (string, error) {
	contents, err := os.ReadFile(value.Agent.APIKeyFile)
	if err != nil {
		return "", errors.New("agent.api_key_file could not be read")
	}
	// The key string is a copy; clearing the file buffer shortens the window the
	// secret spends in memory as bytes.
	defer clear(contents)
	apiKey := strings.TrimSpace(string(contents))
	if apiKey == "" {
		return "", errors.New("agent.api_key_file is empty")
	}
	return apiKey, nil
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
	return value.validateAgent()
}

// validateAgent rejects an agent configuration Core cannot start the required
// agent with. Validation stays static: it never reads the secret file, which
// startup reports separately and without the configured path.
func (value Config) validateAgent() error {
	if strings.TrimSpace(value.Agent.APIKeyFile) == "" {
		return errors.New("agent.api_key_file is required")
	}
	if value.Agent.BaseURL != "" {
		parsed, err := url.Parse(value.Agent.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("agent.base_url must be an absolute http or https URL")
		}
	}
	efforts := agentReasoningEfforts()
	if value.Agent.ReasoningEffort != "" && !slices.Contains(efforts, value.Agent.ReasoningEffort) {
		return fmt.Errorf(
			"agent.reasoning_effort must be one of %s", strings.Join(efforts, ", "),
		)
	}
	if value.Agent.MaxSteps < 0 {
		return errors.New("agent.max_steps must not be negative")
	}
	if value.Agent.HistoryRetention != 0 &&
		value.Agent.HistoryRetention < MinimumAgentHistoryRetention {
		return fmt.Errorf(
			"agent.history_retention must be at least %s", MinimumAgentHistoryRetention,
		)
	}
	return nil
}
