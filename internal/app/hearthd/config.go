package hearthd

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Embed household timezone rules instead of requiring host zoneinfo.

	"github.com/cloudwego/eino/components/model"
	"gopkg.in/yaml.v3"

	"github.com/mholtzscher/hearth/internal/modules/agent"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/household"
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
	// DefaultAgentModel when agent.reasoning_effort is unset. Keep the existing
	// default explicit; operators can enable reasoning through the Responses API.
	DefaultAgentReasoningEffort = "none"
	// DefaultAgentHistoryRetention bounds how long Core keeps agent conversations.
	DefaultAgentHistoryRetention = 30 * 24 * time.Hour
	// MinimumAgentHistoryRetention keeps agent conversation retention at the
	// module's own floor: a shorter window would delete more than intended.
	MinimumAgentHistoryRetention = agent.MinimumConversationRetention
)

type Config struct {
	HouseholdTimezone string `yaml:"household_timezone"`
	// HouseholdUnitSystem selects how people see measurements: "metric" or
	// "us". Empty selects metric; a restart applies a change.
	HouseholdUnitSystem string `yaml:"household_unit_system"`
	HTTPAddr            string `yaml:"http_addr"`
	NATSURL             string `yaml:"nats_url"`
	SQLitePath          string `yaml:"sqlite_path"`
	// ObservationRetention bounds how long Core keeps non-current observations.
	// Zero selects DefaultObservationRetention; a restart applies policy changes
	// on the startup prune pass.
	ObservationRetention time.Duration `yaml:"observation_retention"`
	// AutomationHistoryRetention bounds how long Core keeps terminal Automation
	// Runs and Skips. Zero selects DefaultAutomationHistoryRetention; a restart
	// applies policy changes on the startup prune pass.
	AutomationHistoryRetention time.Duration `yaml:"automation_history_retention"`
	// Agent is the required household agent configuration. Core always
	// constructs the agent; its credential is loaded separately at startup.
	Agent AgentConfig `yaml:"agent"`
}

// AgentConfig configures the required household agent without credentials.
// The model API key is supplied through the selected provider's environment variable.
type AgentConfig struct {
	// Provider selects openai (the default) or opencode-go.
	Provider agent.Provider `yaml:"provider"`
	// Model names the chat model. Empty selects the provider's Hearth default; a restart
	// applies a change to later turns.
	Model string `yaml:"model"`
	// BaseURL overrides the model provider endpoint. Empty uses the provider's
	// own default; a set value must be an absolute http or https URL.
	BaseURL string `yaml:"base_url"`
	// ReasoningEffort selects the model's reasoning level from
	// the selected provider. Empty selects none for OpenAI's DefaultAgentModel,
	// otherwise the provider's own default.
	ReasoningEffort string `yaml:"reasoning_effort"`
	// ServiceTier selects OpenAI's processing tier, such as fast, for every
	// agent request. Empty uses the OpenAI project's default tier. Only the
	// openai provider accepts it.
	ServiceTier string `yaml:"service_tier"`
	// MaxIterations bounds one turn's model-generation cycles. Zero selects the
	// agent module's default of 10.
	MaxIterations int `yaml:"max_iterations"`
	// HistoryRetention bounds how long Core keeps whole agent conversations.
	// Zero selects DefaultAgentHistoryRetention; a restart applies policy
	// changes on the startup prune pass.
	HistoryRetention time.Duration `yaml:"history_retention"`
	// ChatModel replaces the provider chat model the agent would construct,
	// which lets tests and embeddings run a turn without a live provider. It
	// is deliberately not YAML configuration.
	ChatModel      model.ToolCallingChatModel `yaml:"-"`
	legacyMaxSteps bool
}

// UnmarshalYAML retains the presence of the removed setting, including null,
// without changing the policy of ignoring other unknown keys.
func (value *AgentConfig) UnmarshalYAML(node *yaml.Node) error {
	type agentConfig AgentConfig
	decoded := struct {
		*agentConfig `yaml:",inline"`

		MaxSteps yaml.Node `yaml:"max_steps"`
	}{agentConfig: (*agentConfig)(value)}
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	value.legacyMaxSteps = decoded.MaxSteps.Kind != 0
	return nil
}

// NormalizeConfig applies Core's post-resolution defaults. Call after all
// configuration sources have been resolved and before validation.
func NormalizeConfig(value Config) Config {
	if value.ObservationRetention == 0 {
		value.ObservationRetention = DefaultObservationRetention
	}
	if value.AutomationHistoryRetention == 0 {
		value.AutomationHistoryRetention = DefaultAutomationHistoryRetention
	}
	value.Agent.Provider = value.Agent.Provider.Effective()
	value.Agent.Model = value.EffectiveAgentModel()
	if value.Agent.ReasoningEffort == "" && value.Agent.Provider == agent.ProviderOpenAI &&
		value.Agent.Model == DefaultAgentModel {
		value.Agent.ReasoningEffort = DefaultAgentReasoningEffort
	}
	if value.Agent.HistoryRetention == 0 {
		value.Agent.HistoryRetention = DefaultAgentHistoryRetention
	}
	return value
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
// the selected provider's Hearth default when the setting is unset.
func (value Config) EffectiveAgentModel() string {
	if value.Agent.Model == "" {
		if value.Agent.Provider.Effective() == agent.ProviderOpenCodeGo {
			return agent.DefaultGoModel
		}
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

// LoadAgentAPIKey reads only the selected provider's Hearth environment variable.
// Startup calls it once; the credential never enters serializable Config.
// Credential errors name only the variable, never its value or other environment data.
func (value Config) LoadAgentAPIKey() (string, error) {
	if err := value.agentModelSettings().ValidateSettings(); err != nil {
		return "", err
	}
	variable := "HEARTH_OPENAI_API_KEY"
	if value.Agent.Provider.Effective() == agent.ProviderOpenCodeGo {
		variable = "HEARTH_OPENCODE_GO_API_KEY"
	}
	apiKey := strings.TrimSpace(os.Getenv(variable))
	if apiKey == "" {
		return "", fmt.Errorf("%s is required", variable)
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
	if _, err := household.ParseUnitSystem(value.HouseholdUnitSystem); err != nil {
		return err
	}
	_, portText, err := net.SplitHostPort(value.HTTPAddr)
	if err != nil {
		return errors.New("http_addr must contain a valid host and port")
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
// agent with. Validation stays static: startup validates the environment
// credential separately at the agent stage.
func (value Config) validateAgent() error {
	if value.Agent.legacyMaxSteps {
		return errors.New("agent.max_steps was removed; replace it with agent.max_iterations (model-generation cycles)")
	}
	if err := value.agentModelSettings().ValidateSettings(); err != nil {
		return err
	}
	if value.Agent.MaxIterations < 0 {
		return errors.New("agent.max_iterations must not be negative")
	}
	if value.Agent.HistoryRetention != 0 &&
		value.Agent.HistoryRetention < MinimumAgentHistoryRetention {
		return fmt.Errorf(
			"agent.history_retention must be at least %s", MinimumAgentHistoryRetention,
		)
	}
	return nil
}

func (value Config) agentModelSettings() agent.ModelConfig {
	return agent.ModelConfig{
		Provider:        value.Agent.Provider.Effective(),
		Model:           value.EffectiveAgentModel(),
		BaseURL:         value.Agent.BaseURL,
		ReasoningEffort: value.Agent.ReasoningEffort,
		ServiceTier:     value.Agent.ServiceTier,
	}
}
