package agent

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3/responses"
)

// Provider selects the model connection, not the agent runtime.
type Provider string

const (
	ProviderOpenAI     Provider = "openai"
	ProviderOpenCodeGo Provider = "opencode-go"
	DefaultGoModel              = "deepseek-v4.1-flash"
	DefaultGoBaseURL            = "https://opencode.ai/inference/go/openai/v1"
)

// transientStatusRetries lets the SDK repeat a request once after a connection
// failure or a 408, 409, 429, or 5xx status, honoring Retry-After. The retry
// completes before any response streams, so no tool call is announced or run
// twice. Malformed, truncated, and rejected responses are never retried.
const transientStatusRetries = 1

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
	// ServiceTier selects OpenAI's processing tier, such as fast. Empty omits
	// it, so the OpenAI project's default tier applies.
	ServiceTier string
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
	return config.validateServiceTier()
}

func (config ModelConfig) validateServiceTier() error {
	if config.ServiceTier == "" {
		return nil
	}
	if config.Provider.Effective() != ProviderOpenAI {
		return errors.New("agent.service_tier applies only to the openai provider")
	}
	// The pinned SDK's tiers. OpenAI decides which ones each model supports.
	tiers := []string{
		string(responses.ResponseNewParamsServiceTierAuto),
		string(responses.ResponseNewParamsServiceTierDefault),
		string(responses.ResponseNewParamsServiceTierFlex),
		string(responses.ResponseNewParamsServiceTierScale),
		string(responses.ResponseNewParamsServiceTierPriority),
		string(responses.ResponseNewParamsServiceTierFast),
		string(responses.ResponseNewParamsServiceTierUltrafast),
	}
	if !slices.Contains(tiers, config.ServiceTier) {
		return fmt.Errorf("agent.service_tier must be one of %s", strings.Join(tiers, ", "))
	}
	return nil
}
