package agent //nolint:testpackage // Exercises both private provider boundaries with synthetic SSE.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
)

func TestProvidersRetainReportedCacheUsage(t *testing.T) {
	t.Parallel()
	for _, provider := range []Provider{ProviderOpenAI, ProviderOpenCodeGo} {
		for _, test := range []struct {
			name    string
			details string
			cached  *int64
			written *int64
		}{
			{name: "cache hit and write", details: `{"cached_tokens":1536,"cache_write_tokens":512}`, cached: new(int64(1536)), written: new(int64(512))},
			{name: "explicit miss", details: `{"cached_tokens":0,"cache_write_tokens":0}`, cached: new(int64(0)), written: new(int64(0))},
			{name: "Go gateway shape", details: `{"cached_tokens":1536,"cache_write_tokens":null}`, cached: new(int64(1536))},
			{name: "absent", details: `{}`},
			{name: "null", details: `null`},
		} {
			t.Run(string(provider)+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				message := generateUsageFixture(t, provider, test.details, true)
				usage, ok := message.Extra[modelUsageKey].(modelUsage)
				require.True(t, ok)
				require.Equal(t, modelUsage{
					Provider: provider, Model: "usage-fixture",
					InputTokens: new(int64(2048)), OutputTokens: new(int64(16)), TotalTokens: new(int64(2064)),
					CachedInputTokens: test.cached, CacheWriteInputTokens: test.written,
				}, usage)
				require.Equal(t, 2048, message.ResponseMeta.Usage.PromptTokens)
				if test.cached != nil {
					require.Equal(t, int(*test.cached), message.ResponseMeta.Usage.PromptTokenDetails.CachedTokens)
				}
				if test.written != nil {
					require.Equal(t, int(*test.written), message.ResponseMeta.Usage.PromptTokenDetails.CacheWriteTokens)
				}
			})
		}
	}
}

func TestProvidersDoNotInventMissingUsage(t *testing.T) {
	t.Parallel()
	for _, provider := range []Provider{ProviderOpenAI, ProviderOpenCodeGo} {
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			message := generateUsageFixture(t, provider, "", false)
			require.Equal(t, modelUsage{Provider: provider, Model: "usage-fixture"}, message.Extra[modelUsageKey])
		})
	}
}

func generateUsageFixture(t *testing.T, provider Provider, details string, includeUsage bool) *schema.Message {
	t.Helper()
	transport := option.WithHTTPClient(
		&http.Client{Transport: responsesRoundTripFunc(func(*http.Request) (*http.Response, error) {
			body := usageStreamFixture(provider, details, includeUsage)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
	)
	config := ModelConfig{Provider: provider, APIKey: "private-api-key", Model: "usage-fixture"}
	var chat model.BaseChatModel
	if provider == ProviderOpenAI {
		adapter := newResponsesModel(config)
		adapter.client.Options = append(adapter.client.Options, transport)
		chat = adapter
	} else {
		adapter := newGoChatModel(config)
		adapter.client.Options = append(adapter.client.Options, transport)
		chat = adapter
	}
	ctx := context.WithValue(t.Context(), modelConversationKey{}, "aconv_usage")
	message, err := chat.Generate(ctx, []*schema.Message{schema.UserMessage("private-user-prompt")})
	require.NoError(t, err)
	return message
}

// Synthetic frames use the documented OpenAI usage shape and the standard
// prompt_tokens_details shape observed from Go on 2026-11-10. Not a cache test.
func usageStreamFixture(provider Provider, details string, includeUsage bool) string {
	if provider == ProviderOpenAI {
		usage := ""
		if includeUsage {
			usage = `,"usage":{"input_tokens":2048,"output_tokens":16,"total_tokens":2064,"input_tokens_details":` + details + `}`
		}
		return responsesStream(
			`{"id":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"private-answer"}]}]` + usage + `}`,
		)
	}
	body := "data: {\"id\":\"response\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"private-answer\",\"reasoning_content\":\"private-reasoning\"},\"finish_reason\":\"stop\"}],\"usage\":null}\n\n"
	if includeUsage {
		body += `data: {"id":"response","choices":[],"usage":{"prompt_tokens":2048,"completion_tokens":16,"total_tokens":2064,"prompt_tokens_details":` + details + `}}` + "\n\n"
	}
	return body + "data: [DONE]\n\n"
}
