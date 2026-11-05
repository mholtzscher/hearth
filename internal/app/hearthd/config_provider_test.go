package hearthd_test

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
	"github.com/mholtzscher/hearth/internal/modules/agent"
)

func TestGoProviderDefaultsAndReasoning(t *testing.T) {
	t.Parallel()
	config := validConfig()
	config.Agent.Provider = agent.ProviderOpenCodeGo
	got := hearthd.NormalizeConfig(config)
	if got.Agent.Model != "deepseek-v4.1-flash" || got.Agent.ReasoningEffort != "" ||
		got.Agent.Provider != agent.ProviderOpenCodeGo {
		t.Fatalf("Go defaults: %+v", got.Agent)
	}
	if config.EffectiveAgentModel() != "deepseek-v4.1-flash" {
		t.Fatal("unnormalized Go config used the OpenAI default")
	}
	for _, effort := range []string{"", "low", "high", "max"} {
		got.Agent.ReasoningEffort = effort
		if err := got.Validate(); err != nil {
			t.Fatalf("Go effort %q: %v", effort, err)
		}
	}
	for _, effort := range []string{"none", "minimal", "medium", "MAX"} {
		got.Agent.ReasoningEffort = effort
		if err := got.Validate(); err == nil {
			t.Fatalf("Go effort %q accepted", effort)
		}
	}
	config.Agent.Provider = "private-unknown-provider"
	if err := config.Validate(); err == nil || strings.Contains(err.Error(), "private-unknown-provider") {
		t.Fatalf("unknown provider error: %v", err)
	}
}

func TestGoCredentialSelectionAndRedaction(t *testing.T) {
	t.Setenv("HEARTH_OPENAI_API_KEY", "other-provider-key")
	t.Setenv("HEARTH_OPENCODE_GO_API_KEY", "  go-secret-key\n")
	config := validConfig()
	config.Agent.Provider = agent.ProviderOpenCodeGo
	key, err := config.LoadAgentAPIKey()
	if err != nil || key != "go-secret-key" {
		t.Fatal("Go credential was not selected and trimmed")
	}
	raw, err := yaml.Marshal(config)
	if err != nil || strings.Contains(string(raw), "go-secret-key") {
		t.Fatal("Go credential entered configuration")
	}
	for _, value := range []string{"", " \t\n"} {
		t.Setenv("HEARTH_OPENCODE_GO_API_KEY", value)
		key, err = config.LoadAgentAPIKey()
		if key != "" || err == nil || err.Error() != "HEARTH_OPENCODE_GO_API_KEY is required" {
			t.Fatal("Go credential fell back to OpenAI or exposed data")
		}
		if err = config.Validate(); err != nil {
			t.Fatalf("static validation read the Go credential: %v", err)
		}
	}
	t.Setenv("HEARTH_OPENAI_API_KEY", "")
	t.Setenv("HEARTH_OPENCODE_GO_API_KEY", "go-secret-key")
	config.Agent.Provider = agent.ProviderOpenAI
	if key, err = config.LoadAgentAPIKey(); key != "" || err == nil ||
		err.Error() != "HEARTH_OPENAI_API_KEY is required" {
		t.Fatal("OpenAI credential fell back to Go")
	}
}
