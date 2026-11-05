package agent

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Provider selects the model connection, not the agent runtime.
type Provider string

const (
	ProviderOpenAI     Provider = "openai"
	ProviderOpenCodeGo Provider = "opencode-go"
	DefaultGoModel              = "deepseek-v4.1-flash"
	DefaultGoBaseURL            = "https://opencode.ai/inference/go/openai/v1"
)

// Effective preserves the original OpenAI connection when no provider is set.
func (provider Provider) Effective() Provider {
	if provider == "" {
		return ProviderOpenAI
	}
	return provider
}

// ModelConfig carries provider settings and separately loaded credentials.
type ModelConfig struct {
	Provider        Provider
	APIKey          string
	Model           string
	BaseURL         string
	ReasoningEffort string
}

// ValidateSettings checks non-secret settings without reading credentials.
// Go currently targets DeepSeek V4.1 Flash's Chat Completions contract.
func (config ModelConfig) ValidateSettings() error {
	var efforts []string
	switch config.Provider.Effective() {
	case ProviderOpenAI:
		efforts = []string{"none", "minimal", "low", "medium", "high"}
	case ProviderOpenCodeGo:
		efforts = []string{"low", "high", "max"}
	default:
		return errors.New("agent.provider must be openai or opencode-go")
	}
	if config.BaseURL != "" {
		parsed, err := url.Parse(config.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("agent.base_url must be an absolute http or https URL")
		}
	}
	if config.ReasoningEffort != "" && !slices.Contains(efforts, config.ReasoningEffort) {
		return fmt.Errorf("agent.reasoning_effort must be one of %s", strings.Join(efforts, ", "))
	}
	return nil
}
