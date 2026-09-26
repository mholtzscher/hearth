package hearthd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
)

// testAgentAPIKeyFile is the path-only agent secret reference the config tests
// use; no test reads it unless it also writes the file.
const testAgentAPIKeyFile = "agent-api-key"

// testAgentConfig is the minimal agent block Core requires to start.
func testAgentConfig() hearthd.AgentConfig {
	return hearthd.AgentConfig{APIKeyFile: testAgentAPIKeyFile}
}

func TestLoadExampleConfig(t *testing.T) {
	t.Parallel()
	value, err := hearthd.LoadConfig(filepath.Join("..", "..", "..", "configs", "hearthd.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if value.HTTPAddr != "127.0.0.1:8080" {
		t.Fatalf("http_addr = %q", value.HTTPAddr)
	}
}

func TestSimulatorCoreConfigAcceptsCLIOverridesBeforeValidation(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "..", "configs", "hearthd.simulator.yaml")
	if _, err := hearthd.LoadConfig(path); err == nil {
		t.Fatal("incomplete simulator Core config passed without deployment overrides")
	}
	config, err := hearthd.LoadConfigWithOverrides(path, hearthd.ConfigOverrides{
		HTTPAddr: "127.0.0.1:8140", NATSURL: "nats://127.0.0.1:4282",
		SQLitePath: ".data/simulator-stack/storage/hearthd.db", APIKeyFile: "agent-api-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.HTTPAddr != "127.0.0.1:8140" || config.NATSURL != "nats://127.0.0.1:4282" ||
		config.SQLitePath != ".data/simulator-stack/storage/hearthd.db" || config.Agent.APIKeyFile != "agent-api-key" {
		t.Fatalf("worktree overrides were not applied: %+v", config)
	}
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

func TestLoadConfigDefaultsObservationRetention(t *testing.T) {
	t.Parallel()
	value := loadRetentionConfig(t, "")
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

func TestLoadConfigParsesExplicitObservationRetention(t *testing.T) {
	t.Parallel()
	// Eight days is the minimum: it stays above the seven-day JetStream retention.
	value := loadRetentionConfig(t, "observation_retention: 192h\n")
	if value.ObservationRetention != 192*time.Hour {
		t.Fatalf("observation_retention = %s, want 192h", value.ObservationRetention)
	}
}

func TestLoadConfigExplicitZeroObservationRetentionSelectsDefault(t *testing.T) {
	t.Parallel()
	// Zero keeps its documented default meaning: an explicit zero is
	// indistinguishable from unset and selects the default.
	value := loadRetentionConfig(t, "observation_retention: 0s\n")
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

func TestLoadConfigRejectsObservationRetentionBelowMinimum(t *testing.T) {
	t.Parallel()
	short := hearthd.Config{HouseholdTimezone: "UTC",
		HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
		ObservationRetention: 7 * 24 * time.Hour,
		Agent:                testAgentConfig(),
	}
	if err := short.Validate(); err == nil {
		t.Fatal("seven-day observation retention unexpectedly accepted")
	}
	path := writeRetentionConfig(t, "observation_retention: 168h\n")
	if _, err := hearthd.LoadConfig(path); err == nil {
		t.Fatal("seven-day observation retention file unexpectedly accepted")
	}
}

func TestLoadConfigDefaultsAutomationHistoryRetention(t *testing.T) {
	t.Parallel()
	value := loadRetentionConfig(t, "")
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

func TestLoadConfigParsesExplicitAutomationHistoryRetention(t *testing.T) {
	t.Parallel()
	value := loadRetentionConfig(t, "automation_history_retention: 192h\n")
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

func TestLoadExampleConfigDocumentsAutomationHistoryRetention(t *testing.T) {
	t.Parallel()
	value, err := hearthd.LoadConfig(filepath.Join("..", "..", "..", "configs", "hearthd.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if value.AutomationHistoryRetention != 720*time.Hour {
		t.Fatalf("example automation_history_retention = %s, want 720h", value.AutomationHistoryRetention)
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

// TestConfigRequiresAgentAPIKeyFile protects the required-agent contract: Core
// always constructs the agent, so a configuration without the secret file is
// invalid at load time instead of starting a credential-less agent. It fails if
// the requirement is dropped or an empty path is accepted.
func TestConfigRequiresAgentAPIKeyFile(t *testing.T) {
	t.Parallel()
	for _, agentConfig := range []hearthd.AgentConfig{
		{},
		{APIKeyFile: "   "},
	} {
		value := hearthd.Config{HouseholdTimezone: "UTC",
			HTTPAddr: "127.0.0.1:8080", NATSURL: "nats://127.0.0.1:4222", SQLitePath: "hearth.db",
			Agent: agentConfig,
		}
		if err := value.Validate(); err == nil {
			t.Fatalf("agent configuration %+v unexpectedly accepted", agentConfig)
		}
	}
	// The file-based path rejects it too, before any startup work.
	path := writeRetentionConfig(t, "")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	withoutAgent := strings.Replace(
		string(contents), "agent:\n  api_key_file: "+testAgentAPIKeyFile+"\n", "", 1,
	)
	if withoutAgent == string(contents) {
		t.Fatal("test fixture no longer carries an agent block")
	}
	missing := filepath.Join(t.TempDir(), "hearth.yaml")
	if writeErr := os.WriteFile(missing, []byte(withoutAgent), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if _, loadErr := hearthd.LoadConfig(missing); loadErr == nil {
		t.Fatal("configuration without an agent block unexpectedly accepted")
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

// TestLoadConfigDefaultsAgentSettings protects the documented agent defaults:
// the household model and a thirty-day conversation window, with the module
// floor as the minimum accepted window. It fails if a default or floor drifts.
func TestLoadConfigDefaultsAgentSettings(t *testing.T) {
	t.Parallel()
	value := loadRetentionConfig(t, "")
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
	value := loadRetentionConfig(t, "")
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
	contents := "http_addr: 127.0.0.1:8080\nnats_url: nats://127.0.0.1:4222\n" +
		"sqlite_path: hearth.db\nhousehold_timezone: UTC\n" +
		"agent:\n  api_key_file: " + testAgentAPIKeyFile + "\n  model: other-household-model\n"
	path := filepath.Join(t.TempDir(), "hearth.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	other, err := hearthd.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
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

// TestLoadAgentAPIKeyReadsTheSecretFile protects the credential seam: the key is
// read from the configured file with surrounding whitespace removed, so a
// trailing newline never reaches the provider.
func TestLoadAgentAPIKeyReadsTheSecretFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "agent-api-key")
	if err := os.WriteFile(path, []byte("  test-secret-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value := hearthd.Config{Agent: hearthd.AgentConfig{APIKeyFile: path}}
	apiKey, err := value.LoadAgentAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if apiKey != "test-secret-key" {
		t.Fatalf("api key = %q, want the trimmed secret", apiKey)
	}
}

// TestLoadAgentAPIKeyFailuresDoNotLeakThePath protects startup diagnostics: an
// unreadable or empty secret file classifies the failure without repeating the
// configured path or the file's contents.
func TestLoadAgentAPIKeyFailuresDoNotLeakThePath(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "secret-do-not-log", "agent-api-key")
	for _, path := range []string{missing, ""} {
		value := hearthd.Config{Agent: hearthd.AgentConfig{APIKeyFile: path}}
		_, err := value.LoadAgentAPIKey()
		if err == nil {
			t.Fatalf("api key file %q unexpectedly loaded", path)
		}
		if path != "" && strings.Contains(err.Error(), path) {
			t.Fatalf("error %q repeated the configured path", err)
		}
		if strings.Contains(err.Error(), "secret-do-not-log") {
			t.Fatalf("error %q repeated a path segment", err)
		}
	}
	empty := filepath.Join(t.TempDir(), "agent-api-key")
	if err := os.WriteFile(empty, []byte("\n\t"), 0o600); err != nil {
		t.Fatal(err)
	}
	value := hearthd.Config{Agent: hearthd.AgentConfig{APIKeyFile: empty}}
	if _, err := value.LoadAgentAPIKey(); err == nil {
		t.Fatal("empty api key file unexpectedly accepted")
	} else if strings.Contains(err.Error(), empty) {
		t.Fatalf("error %q repeated the configured path", err)
	}
}

func loadRetentionConfig(t *testing.T, retentionLine string) hearthd.Config {
	t.Helper()
	value, err := hearthd.LoadConfig(writeRetentionConfig(t, retentionLine))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func writeRetentionConfig(t *testing.T, retentionLine string) string {
	t.Helper()
	contents := "http_addr: 127.0.0.1:8080\nnats_url: nats://127.0.0.1:4222\n" +
		"sqlite_path: hearth.db\nhousehold_timezone: UTC\n" + retentionLine +
		"agent:\n  api_key_file: " + testAgentAPIKeyFile + "\n"
	path := filepath.Join(t.TempDir(), "hearth.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
