package agent //nolint:testpackage // Exercises the private provider without a listener.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func TestGoDefaultEndpointAndProviderDefaults(t *testing.T) {
	t.Parallel()
	provider := newGoChatModel(ModelConfig{APIKey: "test-key", Model: DefaultGoModel})
	provider.client.Options = append(provider.client.Options, option.WithHTTPClient(&http.Client{
		Transport: responsesRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != DefaultGoBaseURL+"/chat/completions" ||
				request.Header.Get("Authorization") != "Bearer test-key" ||
				request.Header.Get("User-Agent") != "hearth/1.0" ||
				request.Header.Get("X-Opencode-Session") != "aconv_fixture" {
				t.Error("wrong Go endpoint, identity, credential, or session")
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if _, exists := body["reasoning_effort"]; exists {
				t.Error("unset effort overrode the provider default")
			}
			if body["model"] != "deepseek-v4.1-flash" || body["stream"] != true {
				t.Fatalf("wrong request: %v", body)
			}
			return &http.Response{StatusCode: http.StatusOK,
				Header: http.Header{"Content-Type": {"text/event-stream"}},
				Body:   io.NopCloser(strings.NewReader(goChatStream("private", "Ready.", "", "stop"))),
			}, nil
		}),
	}))
	ctx := context.WithValue(t.Context(), modelConversationKey{}, "aconv_fixture")
	message, err := provider.Generate(ctx, []*schema.Message{schema.UserMessage("Hello.")})
	if err != nil || message.Content != "Ready." || message.ReasoningContent != "" ||
		message.ResponseMeta.Usage.TotalTokens != 15 || message.ResponseMeta.Usage.PromptTokenDetails.CachedTokens != 8 {
		t.Fatalf("message=%+v error=%v", message, err)
	}
	if _, err = provider.Generate(t.Context(), []*schema.Message{schema.UserMessage("No session")}); err == nil {
		t.Fatal("request without a conversation identity accepted")
	}
}

func TestGoToolPreparationPrecedesCompletion(t *testing.T) {
	t.Parallel()
	var output goChatAccumulator
	var announcements int
	ctx := withToolPreparing(t.Context(), func(id, name string) {
		announcements++
		if output.finishReason != "" || id != "call_view" || name != "present_view" {
			t.Errorf("preparation identity or timing: %s/%s", id, name)
		}
	})
	for _, raw := range []string{
		`{"id":"response","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_view","type":"function","function":{"name":"present_view","arguments":"{"}}]}}]}`,
		`{"id":"response","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]}}]}`,
		`{"id":"response","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	} {
		var chunk openai.ChatCompletionChunk
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			t.Fatal(err)
		}
		if err := output.add(ctx, chunk); err != nil {
			t.Fatal(err)
		}
	}
	message, err := output.message()
	if err != nil || announcements != 1 || len(message.ToolCalls) != 1 || message.ToolCalls[0].ID != "call_view" ||
		!output.announced["call_view"] {
		t.Fatalf("message=%+v error=%v", message, err)
	}
}

// Synthetic Chat Completions frames. They prove our protocol handling, not
// acceptance by OpenCode Go or DeepSeek.
func goChatStream(reasoning, content, calls, finish string) string {
	if calls == "" {
		calls = "[]"
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(calls)); err != nil {
		panic(err)
	}
	reasonJSON, _ := json.Marshal(reasoning)
	contentJSON, _ := json.Marshal(content)
	return fmt.Sprintf(
		"data: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":%s,\"content\":%s,\"tool_calls\":%s}}]}\n\n",
		reasonJSON,
		contentJSON,
		compact.String(),
	) +
		fmt.Sprintf(
			"data: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\n",
			finish,
		) +
		"data: {\"id\":\"fixture\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15," +
		"\"prompt_tokens_details\":{\"cached_tokens\":8,\"cache_write_tokens\":null}}}\n\ndata: [DONE]\n\n"
}
