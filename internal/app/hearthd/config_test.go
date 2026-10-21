package hearthd_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
)

// testAgentConfig uses defaults for the required agent's non-secret settings.
func testAgentConfig() hearthd.AgentConfig {
	return hearthd.AgentConfig{}
}

func TestConfigAcceptsNonLoopbackHTTP(t *testing.T) {
	t.Parallel()
	value := hearthd.Config{
		HouseholdTimezone: "UTC",
		HTTPAddr:          "0.0.0.0:8080",
		NATSURL:           "nats://127.0.0.1:4222",
		SQLitePath:        "hearth.db",
		Agent:             testAgentConfig(),
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("validate non-loopback HTTP address: %v", err)
	}
}

func TestNormalizeConfigDefaultsObservationRetention(t *testing.T) {
	t.Parallel()
	value := hearthd.NormalizeConfig(validConfig())
	if value.ObservationRetention != hearthd.DefaultObservationRetention {
		t.Fatalf(
			"observation_retention = %s, want default %s",
			value.ObservationRetention,
			hearthd.DefaultObservationRetention,
		)
	}
	if hearthd.DefaultObservationRetention != 30*24*time.Hour {
		t.Fatalf("default observation retention = %s, want 720h", hearthd.DefaultObservationRetention)
	}
}

func TestNormalizeConfigPreservesExplicitObservationRetention(t *testing.T) {
	t.Parallel()
	// Eight days is the minimum: it stays above the seven-day JetStream retention.
	config := validConfig()
	config.ObservationRetention = 192 * time.Hour
	value := hearthd.NormalizeConfig(config)
	if value.ObservationRetention != 192*time.Hour {
		t.Fatalf("observation_retention = %s, want 192h", value.ObservationRetention)
	}
}

func TestNormalizeConfigExplicitZeroObservationRetentionSelectsDefault(t *testing.T) {
	t.Parallel()
	// Zero keeps its documented default meaning: an explicit zero is
	// indistinguishable from unset and selects the default.
	value := hearthd.NormalizeConfig(validConfig())
	if value.ObservationRetention != hearthd.DefaultObservationRetention {
		t.Fatalf(
			"observation_retention = %s, want default %s",
			value.ObservationRetention,
			hearthd.DefaultObservationRetention,
		)
	}
}

func TestObservationRetentionRejectsNegativeAndJustBelowMinimum(t *testing.T) {
	t.Parallel()
	if hearthd.MinimumObservationRetention != 8*24*time.Hour {
		t.Fatalf(
			"minimum observation retention = %s, want 192h",
			hearthd.MinimumObservationRetention,
		)
	}
	for _, retention := range []time.Duration{
		-time.Hour,
		hearthd.MinimumObservationRetention - time.Nanosecond,
	} {
		value := hearthd.Config{HouseholdTimezone: "UTC",
			HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
			ObservationRetention: retention,
			Agent:                testAgentConfig(),
		}
		if err := value.Validate(); err == nil {
			t.Fatalf("observation retention %s unexpectedly accepted", retention)
		}
	}
}

func TestConfigRejectsObservationRetentionBelowMinimum(t *testing.T) {
	t.Parallel()
	short := hearthd.Config{HouseholdTimezone: "UTC",
		HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
		ObservationRetention: 7 * 24 * time.Hour,
		Agent:                testAgentConfig(),
	}
	if err := short.Validate(); err == nil {
		t.Fatal("seven-day observation retention unexpectedly accepted")
	}
}

func TestNormalizeConfigDefaultsAutomationHistoryRetention(t *testing.T) {
	t.Parallel()
	value := hearthd.NormalizeConfig(validConfig())
	if value.AutomationHistoryRetention != hearthd.DefaultAutomationHistoryRetention {
		t.Fatalf(
			"automation_history_retention = %s, want default %s",
			value.AutomationHistoryRetention,
			hearthd.DefaultAutomationHistoryRetention,
		)
	}
	if hearthd.DefaultAutomationHistoryRetention != 30*24*time.Hour {
		t.Fatalf("default automation history retention = %s, want 720h", hearthd.DefaultAutomationHistoryRetention)
	}
	if hearthd.MinimumAutomationHistoryRetention != 8*24*time.Hour {
		t.Fatalf("minimum automation history retention = %s, want 192h", hearthd.MinimumAutomationHistoryRetention)
	}
	if hearthd.AutomationFactMaximumAge != 30*time.Second {
		t.Fatalf("automation fact maximum age = %s, want 30s", hearthd.AutomationFactMaximumAge)
	}
}

func TestNormalizeConfigPreservesExplicitAutomationHistoryRetention(t *testing.T) {
	t.Parallel()
	config := validConfig()
	config.AutomationHistoryRetention = 192 * time.Hour
	value := hearthd.NormalizeConfig(config)
	if value.AutomationHistoryRetention != 192*time.Hour {
		t.Fatalf("automation_history_retention = %s, want 192h", value.AutomationHistoryRetention)
	}
	if got := value.EffectiveAutomationHistoryRetention(); got != 192*time.Hour {
		t.Fatalf("effective automation history retention = %s, want 192h", got)
	}
}

func TestAutomationHistoryRetentionRejectsBelowMinimum(t *testing.T) {
	t.Parallel()
	for _, retention := range []time.Duration{
		-time.Hour,
		hearthd.MinimumAutomationHistoryRetention - time.Nanosecond,
	} {
		value := hearthd.Config{HouseholdTimezone: "UTC",
			HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
			AutomationHistoryRetention: retention,
			Agent:                      testAgentConfig(),
		}
		if err := value.Validate(); err == nil {
			t.Fatalf("automation history retention %s unexpectedly accepted", retention)
		}
	}
	var unset hearthd.Config
	if got := unset.EffectiveAutomationHistoryRetention(); got != hearthd.DefaultAutomationHistoryRetention {
		t.Fatalf("effective unset retention = %s, want default", got)
	}
}

func TestEffectiveObservationRetentionFallsBackToDefault(t *testing.T) {
	t.Parallel()
	var unset hearthd.Config
	if got := unset.EffectiveObservationRetention(); got != hearthd.DefaultObservationRetention {
		t.Fatalf("effective retention = %s, want default %s", got, hearthd.DefaultObservationRetention)
	}
	set := hearthd.Config{HouseholdTimezone: "UTC",
		HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
		ObservationRetention: 8 * 24 * time.Hour,
		Agent:                testAgentConfig(),
	}
	if got := set.EffectiveObservationRetention(); got != 8*24*time.Hour {
		t.Fatalf("effective retention = %s, want 192h", got)
	}
	if err := set.Validate(); err != nil {
		t.Fatalf("minimum observation retention rejected: %v", err)
	}
}

// Static validation must not depend on the process credential.
func TestConfigValidationDoesNotReadCredential(t *testing.T) {
	t.Setenv("HEARTH_OPENAI_API_KEY", "")
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("non-secret configuration rejected: %v", err)
	}
}

// TestConfigRejectsInvalidAgentModelSettings protects static agent validation:
// a base URL must be an absolute http or https URL and a reasoning effort must
// be one of the levels Core forwards. It fails if an unusable endpoint or level
// reaches the model provider.
func TestConfigRejectsInvalidAgentModelSettings(t *testing.T) {
	t.Parallel()
	for _, baseURL := range []string{
		"127.0.0.1:8080", "openai.com", "/v1", "ftp://models.example", "https://", "http://models.example extra",
	} {
		agentConfig := testAgentConfig()
		agentConfig.BaseURL = baseURL
		value := hearthd.Config{HouseholdTimezone: "UTC",
			HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
			Agent: agentConfig,
		}
		if err := value.Validate(); err == nil {
			t.Fatalf("agent base URL %q unexpectedly accepted", baseURL)
		}
	}
	for _, baseURL := range []string{"", "http://127.0.0.1:8080/v1", "https://models.example/v1"} {
		agentConfig := testAgentConfig()
		agentConfig.BaseURL = baseURL
		value := hearthd.Config{HouseholdTimezone: "UTC",
			HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
			Agent: agentConfig,
		}
		if err := value.Validate(); err != nil {
			t.Fatalf("agent base URL %q rejected: %v", baseURL, err)
		}
	}
	for _, effort := range []string{"none", "minimal", "low", "medium", "high"} {
		agentConfig := testAgentConfig()
		agentConfig.ReasoningEffort = effort
		value := hearthd.Config{HouseholdTimezone: "UTC",
			HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
			Agent: agentConfig,
		}
		if err := value.Validate(); err != nil {
			t.Fatalf("agent reasoning effort %q rejected: %v", effort, err)
		}
	}
	for _, effort := range []string{"LOW", " low", "extreme", "medium "} {
		agentConfig := testAgentConfig()
		agentConfig.ReasoningEffort = effort
		value := hearthd.Config{HouseholdTimezone: "UTC",
			HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
			Agent: agentConfig,
		}
		if err := value.Validate(); err == nil {
			t.Fatalf("agent reasoning effort %q unexpectedly accepted", effort)
		}
	}
	// A negative step budget is never a valid bound.
	agentConfig := testAgentConfig()
	agentConfig.MaxSteps = -1
	value := hearthd.Config{HouseholdTimezone: "UTC",
		HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
		Agent: agentConfig,
	}
	if err := value.Validate(); err == nil {
		t.Fatal("negative agent max_steps unexpectedly accepted")
	}
}

// TestNormalizeConfigDefaultsAgentSettings protects the documented agent defaults:
// the household model and a thirty-day conversation window, with the module
// floor as the minimum accepted window. It fails if a default or floor drifts.
func TestNormalizeConfigDefaultsAgentSettings(t *testing.T) {
	t.Parallel()
	value := hearthd.NormalizeConfig(validConfig())
	if value.Agent.Model != hearthd.DefaultAgentModel {
		t.Fatalf("agent model = %q, want %q", value.Agent.Model, hearthd.DefaultAgentModel)
	}
	if hearthd.DefaultAgentModel != "gpt-5.6-luna" {
		t.Fatalf("default agent model = %q, want gpt-5.6-luna", hearthd.DefaultAgentModel)
	}
	if value.Agent.HistoryRetention != hearthd.DefaultAgentHistoryRetention {
		t.Fatalf(
			"agent history retention = %s, want default %s",
			value.Agent.HistoryRetention, hearthd.DefaultAgentHistoryRetention,
		)
	}
	if hearthd.DefaultAgentHistoryRetention != 30*24*time.Hour {
		t.Fatalf("default agent retention = %s, want 720h", hearthd.DefaultAgentHistoryRetention)
	}
	if hearthd.MinimumAgentHistoryRetention != 24*time.Hour {
		t.Fatalf("minimum agent retention = %s, want 24h", hearthd.MinimumAgentHistoryRetention)
	}
	var unset hearthd.Config
	if got := unset.EffectiveAgentModel(); got != hearthd.DefaultAgentModel {
		t.Fatalf("effective unset agent model = %q, want the default", got)
	}
	if got := unset.EffectiveAgentHistoryRetention(); got != hearthd.DefaultAgentHistoryRetention {
		t.Fatalf("effective unset agent retention = %s, want the default", got)
	}
	if value.EffectiveAgentHistoryRetention() != 30*24*time.Hour {
		t.Fatalf("effective agent retention = %s, want 720h", value.EffectiveAgentHistoryRetention())
	}
}

// TestAgentReasoningEffortDefaultsForTheDefaultModel protects the documented
// pairing: the default model rejects function tools unless the effort is none,
// so an omitted reasoning_effort must not leave it unset. It fails if the
// default drifts or a non-default model loses the provider default.
func TestAgentReasoningEffortDefaultsForTheDefaultModel(t *testing.T) {
	t.Parallel()
	value := hearthd.NormalizeConfig(validConfig())
	if hearthd.DefaultAgentReasoningEffort != "none" {
		t.Fatalf("default agent reasoning effort = %q, want none", hearthd.DefaultAgentReasoningEffort)
	}
	if value.Agent.ReasoningEffort != hearthd.DefaultAgentReasoningEffort {
		t.Fatalf(
			"omitted reasoning effort = %q, want %q",
			value.Agent.ReasoningEffort, hearthd.DefaultAgentReasoningEffort,
		)
	}

	// An explicit non-default model keeps the provider's own default.
	otherConfig := validConfig()
	otherConfig.Agent.Model = "other-household-model"
	other := hearthd.NormalizeConfig(otherConfig)
	if other.Agent.ReasoningEffort != "" {
		t.Fatalf(
			"non-default model reasoning effort = %q, want the provider default",
			other.Agent.ReasoningEffort,
		)
	}
}

// TestAgentHistoryRetentionRejectsBelowMinimum protects the module floor: a
// shorter window would delete more conversation history than the module
// intends, so it is invalid configuration rather than a silent clamp.
func TestAgentHistoryRetentionRejectsBelowMinimum(t *testing.T) {
	t.Parallel()
	for _, retention := range []time.Duration{
		-time.Hour, time.Nanosecond, hearthd.MinimumAgentHistoryRetention - time.Nanosecond,
	} {
		agentConfig := testAgentConfig()
		agentConfig.HistoryRetention = retention
		value := hearthd.Config{HouseholdTimezone: "UTC",
			HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
			Agent: agentConfig,
		}
		if err := value.Validate(); err == nil {
			t.Fatalf("agent history retention %s unexpectedly accepted", retention)
		}
	}
	agentConfig := testAgentConfig()
	agentConfig.HistoryRetention = hearthd.MinimumAgentHistoryRetention
	value := hearthd.Config{HouseholdTimezone: "UTC",
		HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
		Agent: agentConfig,
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("minimum agent history retention rejected: %v", err)
	}
}

// The startup seam trims the environment credential without storing it in Config.
func TestLoadAgentAPIKeyReadsEnvironment(t *testing.T) {
	t.Setenv("HEARTH_OPENAI_API_KEY", "  test-secret-key\n")
	value := validConfig()
	apiKey, err := value.LoadAgentAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if apiKey != "test-secret-key" {
		t.Fatal("credential was not trimmed")
	}
	serialized, err := yaml.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{string(serialized), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
		if strings.Contains(output, "test-secret-key") {
			t.Fatal("configuration exposed credential")
		}
	}
}

// Missing, empty, and whitespace credentials have one fixed, secret-safe error.
func TestLoadAgentAPIKeyRejectsAbsentCredential(t *testing.T) {
	t.Setenv("HEARTHD_AGENT_API_KEY_FILE", "legacy-path-do-not-read")
	t.Setenv("OPENAI_API_KEY", "alternative-secret-do-not-use")
	for _, name := range []string{"missing", "empty", "whitespace"} {
		t.Run(name, func(t *testing.T) {
			credential := ""
			if name == "whitespace" {
				credential = " \t\r\n\u2003"
			}
			t.Setenv("HEARTH_OPENAI_API_KEY", credential)
			if name == "missing" {
				if err := os.Unsetenv("HEARTH_OPENAI_API_KEY"); err != nil {
					t.Fatal(err)
				}
			}
			key, err := (hearthd.Config{}).LoadAgentAPIKey()
			if key != "" || err == nil || err.Error() != "HEARTH_OPENAI_API_KEY is required" {
				t.Fatal("expected empty credential and fixed variable-name-only diagnosis")
			}
		})
	}
}

func validConfig() hearthd.Config {
	return hearthd.Config{
		HouseholdTimezone: "UTC", HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222",
		SQLitePath: "hearth.db", Agent: testAgentConfig(),
	}
}
