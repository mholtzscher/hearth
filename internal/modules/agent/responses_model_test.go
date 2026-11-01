package agent //nolint:testpackage // Tests the private provider constructor without network access.

import (
	"io"
	"net/http"
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
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"id":"resp_default","status":"completed","output":[{"id":"msg_default","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Ready.","annotations":[]}]}]}`,
				)),
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
}

type responsesRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip responsesRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}
