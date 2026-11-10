package agent

import (
	"context"
	"log/slog"
	"maps"

	"github.com/cloudwego/eino/schema"
	"github.com/openai/openai-go/v3/packages/respjson"
)

const modelUsageKey = "hearth_model_usage"

// modelUsage retains only reported counters, not provider payloads. Nil means
// unknown; zero means the provider explicitly reported no tokens of that kind.
// It stays private in message Extra alongside native continuation metadata.
type modelUsage struct {
	Provider              Provider `json:"provider"`
	Model                 string   `json:"model"`
	InputTokens           *int64   `json:"input_tokens,omitempty"`
	OutputTokens          *int64   `json:"output_tokens,omitempty"`
	TotalTokens           *int64   `json:"total_tokens,omitempty"`
	CachedInputTokens     *int64   `json:"cached_input_tokens,omitempty"`
	CacheWriteInputTokens *int64   `json:"cache_write_input_tokens,omitempty"`
}

func reportedTokens(value int64, field respjson.Field) *int64 {
	if !field.Valid() || value < 0 {
		return nil
	}
	return &value
}

func retainModelUsage(message *schema.Message, usage modelUsage) {
	message.Extra[modelUsageKey] = usage
	// Eino's counters are useful to runtime consumers but cannot distinguish
	// missing from zero. The private record above preserves that distinction.
	if usage.CachedInputTokens != nil {
		message.ResponseMeta.Usage.PromptTokenDetails.CachedTokens = int(*usage.CachedInputTokens)
	}
	if usage.CacheWriteInputTokens != nil {
		message.ResponseMeta.Usage.PromptTokenDetails.CacheWriteTokens = int(*usage.CacheWriteInputTokens)
	}
}

// withoutModelUsage keeps diagnostics out of the model's history byte budget.
// Leave the pre-existing response metadata intact so instrumentation does not
// change which whole turns fit, and never mutate the persisted message.
func withoutModelUsage(message *schema.Message) *schema.Message {
	if _, exists := message.Extra[modelUsageKey]; !exists {
		return message
	}
	prepared := *message
	prepared.Extra = maps.Clone(message.Extra)
	delete(prepared.Extra, modelUsageKey)
	if message.ResponseMeta != nil && message.ResponseMeta.Usage != nil {
		meta := *message.ResponseMeta
		usage := *meta.Usage
		usage.PromptTokenDetails.CachedTokens = 0
		usage.PromptTokenDetails.CacheWriteTokens = 0
		meta.Usage = &usage
		prepared.ResponseMeta = &meta
	}
	return &prepared
}

func (trace *turnTrace) logModelUsage(ctx context.Context, message *schema.Message) {
	// Only fresh provider output reaches this hook, never reloaded history.
	usage, ok := message.Extra[modelUsageKey].(modelUsage)
	if !ok {
		return
	}
	attrs := []slog.Attr{
		slog.String("event", "agent.model_usage"),
		slog.String("conversation_id", trace.conversationID),
		slog.String("provider", string(usage.Provider)),
		slog.String("model", usage.Model),
	}
	for _, counter := range []struct {
		name  string
		value *int64
	}{
		{"input_tokens", usage.InputTokens},
		{"output_tokens", usage.OutputTokens},
		{"total_tokens", usage.TotalTokens},
		{"cached_input_tokens", usage.CachedInputTokens},
		{"cache_write_input_tokens", usage.CacheWriteInputTokens},
	} {
		if counter.value != nil {
			attrs = append(attrs, slog.Int64(counter.name, *counter.value))
		}
	}
	trace.service.logger.LogAttrs(ctx, slog.LevelDebug, "agent model usage", attrs...)
}
