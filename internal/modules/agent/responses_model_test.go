package agent //nolint:testpackage // Tests the private provider constructor without network access.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/openai/openai-go/v3/option"
)

func TestResponsesDefaultEndpoint(t *testing.T) {
	t.Parallel()
	provider := newResponsesModel(ModelConfig{APIKey: "test-key", Model: "fixture"})
	var requested bool
	provider.client.Options = append(provider.client.Options, option.WithHTTPClient(&http.Client{
		Transport: responsesRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			requested = true
			if got := request.URL.String(); got != "https://api.openai.com/v1/responses" {
				t.Errorf("default provider URL=%s", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(responsesStream(
					`{"id":"resp_default","status":"completed","output":[{"id":"msg_default","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Ready.","annotations":[]}]}],` +
						`"usage":{"input_tokens":2048,"output_tokens":5,"total_tokens":2053,` +
						`"input_tokens_details":{"cached_tokens":1536,"cache_write_tokens":512}}}`,
				))),
			}, nil
		}),
	}))
	message, err := provider.Generate(t.Context(), []*schema.Message{schema.UserMessage("Hello.")})
	if err != nil {
		t.Fatal(err)
	}
	if !requested || message.Content != "Ready." {
		t.Fatalf("requested=%v message=%+v", requested, message)
	}
	if details := message.ResponseMeta.Usage.PromptTokenDetails; message.ResponseMeta.Usage.PromptTokens != 2048 ||
		details.CachedTokens != 1536 || details.CacheWriteTokens != 512 {
		t.Fatalf("usage=%+v", message.ResponseMeta.Usage)
	}
}

type responsesRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip responsesRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestResponsesAnnounceToolCallsBeforeTheResponseCompletes(t *testing.T) {
	t.Parallel()
	provider := newResponsesModel(ModelConfig{APIKey: "test-key", Model: "fixture"})
	var announced []string
	provider.client.Options = append(provider.client.Options, option.WithHTTPClient(&http.Client{
		Transport: responsesRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(responsesStream(
					`{"id":"resp_calls","status":"completed","output":[` +
						`{"id":"fc_view","type":"function_call","call_id":"call_view","name":"present_view","arguments":"{}","status":"completed"},` +
						`{"id":"fc_read","type":"function_call","call_id":"call_read","name":"list_entities","arguments":"{}","status":"completed"}]}`,
				))),
			}, nil
		}),
	}))
	ctx := withToolPreparing(t.Context(), func(callID, name string) { announced = append(announced, callID+":"+name) })
	message, err := provider.Generate(ctx, []*schema.Message{schema.UserMessage("Show a view.")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(announced, []string{"call_view:present_view", "call_read:list_entities"}) {
		t.Fatalf("announced=%v", announced)
	}
	if len(message.ToolCalls) != 2 || message.ToolCalls[0].ID != "call_view" || message.ToolCalls[1].ID != "call_read" {
		t.Fatalf("message=%+v", message)
	}
	if _, err = provider.Generate(
		context.Background(),
		[]*schema.Message{schema.UserMessage("No listener.")},
	); err != nil {
		t.Fatalf("generate without a turn listener: %v", err)
	}
}

// responsesStream renders a response body the way the Responses API streams
// it: each output item is announced as it begins, then a terminal event
// carries the complete response.
func responsesStream(response string) string {
	// One SSE data line per event: fixtures may be indented.
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(response)); err != nil {
		panic(err)
	}
	response = compact.String()
	var parsed struct {
		Status string            `json:"status"`
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal([]byte(response), &parsed); err != nil {
		panic(err)
	}
	var stream strings.Builder
	for index, item := range parsed.Output {
		fmt.Fprintf(
			&stream,
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":%d,\"item\":%s}\n\n",
			index,
			item,
		)
	}
	terminal := "response." + parsed.Status
	if parsed.Status != "failed" && parsed.Status != "incomplete" {
		terminal = "response.completed"
	}
	fmt.Fprintf(&stream, "event: %s\ndata: {\"type\":%q,\"response\":%s}\n\n", terminal, terminal, response)
	return stream.String()
}

func writeResponsesStream(w http.ResponseWriter, response string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, responsesStream(response))
}
