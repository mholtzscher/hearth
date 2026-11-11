package agent

import (
	"context"
	"log/slog"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// turnModel marks failures at the actual model call, not the enclosing runner.
// Turns use Generate only; nil output must be rejected here, before ADK's
// tool-call checker dereferences it. ADK supplies tools through model options.
type turnModel struct {
	model.BaseChatModel

	trace *turnTrace
}

func (boundary turnModel) Generate(
	ctx context.Context,
	messages []*schema.Message,
	options ...model.Option,
) (*schema.Message, error) {
	response, err := boundary.BaseChatModel.Generate(ctx, messages, options...)
	if err != nil {
		return nil, modelError{err: err}
	}
	if response == nil {
		return nil, modelError{err: errModelResponseInvalid}
	}
	if meta := response.ResponseMeta; meta != nil && meta.Usage != nil {
		boundary.trace.service.logger.LogAttrs(ctx, slog.LevelDebug, "agent model usage",
			slog.String("event", "agent.model_usage"),
			slog.String("conversation_id", boundary.trace.conversationID),
			slog.String("provider", string(boundary.trace.service.provider)),
			slog.Int("input_tokens", meta.Usage.PromptTokens),
			slog.Int("cached_input_tokens", meta.Usage.PromptTokenDetails.CachedTokens),
			slog.Int("cache_write_input_tokens", meta.Usage.PromptTokenDetails.CacheWriteTokens),
			slog.Int("output_tokens", meta.Usage.CompletionTokens),
		)
	}
	// Providers validate completion syntax; the turn owns whether silence is a
	// complete answer. Only a committed presentation in this turn permits it.
	if response.Content == "" && len(response.ToolCalls) == 0 && !boundary.trace.hasArtifacts() {
		return nil, modelError{err: errModelResponseInvalid}
	}
	return response, nil
}
