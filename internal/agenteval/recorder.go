package agenteval

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/openai/openai-go/v3"
)

// ErrRequestBudget stops a trial whose Agent exceeded its model-request cap.
var ErrRequestBudget = errors.New("agenteval: model request cap reached")

// RecordedRequest is one model request and its response metadata.
type RecordedRequest struct {
	Turn         int             `json:"turn"`
	Index        int             `json:"index"`
	Messages     json.RawMessage `json:"messages"`
	Tools        []string        `json:"tools"`
	ToolCalls    []string        `json:"tool_calls"`
	Content      string          `json:"content"`
	InputTokens  int             `json:"input_tokens"`
	CachedTokens int             `json:"cached_tokens"`
	OutputTokens int             `json:"output_tokens"`
	HasUsage     bool            `json:"has_usage"`
	ModelSeconds float64         `json:"model_seconds"`
	// Error is a status class only; provider text can quote prompts.
	Error string `json:"error,omitempty"`
}

// Recorder wraps the provider model, records every request, paces requests,
// and enforces a per-trial cap. It is safe for the Agent's concurrent use.
type Recorder struct {
	state *recorderState
	inner model.ToolCallingChatModel
	tools []*schema.ToolInfo
}

type recorderState struct {
	mu            sync.Mutex
	turn          int
	maxRequests   int
	minGap        time.Duration
	lastEnd       time.Time
	requests      []RecordedRequest
	overBudget    bool
	providerError bool
}

// NewRecorder wraps inner. A maxRequests of zero means no cap.
func NewRecorder(inner model.ToolCallingChatModel, maxRequests int, minGap time.Duration) *Recorder {
	return &Recorder{inner: inner, state: &recorderState{maxRequests: maxRequests, minGap: minGap}}
}

// SetTurn tags subsequent requests with the zero-based user turn.
func (recorder *Recorder) SetTurn(turn int) {
	recorder.state.mu.Lock()
	defer recorder.state.mu.Unlock()
	recorder.state.turn = turn
}

// Requests returns a copy of the recorded requests.
func (recorder *Recorder) Requests() []RecordedRequest {
	recorder.state.mu.Lock()
	defer recorder.state.mu.Unlock()
	return append([]RecordedRequest(nil), recorder.state.requests...)
}

// OverBudget reports whether the cap stopped a request.
func (recorder *Recorder) OverBudget() bool {
	recorder.state.mu.Lock()
	defer recorder.state.mu.Unlock()
	return recorder.state.overBudget
}

// ProviderError reports whether any request failed at the provider.
func (recorder *Recorder) ProviderError() bool {
	recorder.state.mu.Lock()
	defer recorder.state.mu.Unlock()
	return recorder.state.providerError
}

// WithTools binds tools on a copy that shares the recording state.
func (recorder *Recorder) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := recorder.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &Recorder{state: recorder.state, inner: bound, tools: tools}, nil
}

// Generate records one request, then forwards it unchanged.
func (recorder *Recorder) Generate(
	ctx context.Context,
	input []*schema.Message,
	options ...model.Option,
) (*schema.Message, error) {
	index, err := recorder.admit(ctx)
	if err != nil {
		return nil, err
	}
	messages, _ := json.Marshal(input)
	settings := model.GetCommonOptions(&model.Options{Tools: recorder.tools}, options...)
	record := RecordedRequest{Index: index, Messages: messages, Tools: toolNames(settings.Tools)}
	started := time.Now()
	response, err := recorder.inner.Generate(ctx, input, options...)
	record.ModelSeconds = time.Since(started).Seconds()
	if err != nil {
		record.Error = providerErrorClass(err)
	} else if response != nil {
		record.Content = response.Content
		for _, call := range response.ToolCalls {
			record.ToolCalls = append(record.ToolCalls, call.Function.Name)
		}
		if meta := response.ResponseMeta; meta != nil && meta.Usage != nil {
			record.HasUsage = true
			record.InputTokens = meta.Usage.PromptTokens
			record.CachedTokens = meta.Usage.PromptTokenDetails.CachedTokens
			record.OutputTokens = meta.Usage.CompletionTokens
		}
	}
	recorder.finish(record, err)
	return response, err
}

// Stream is unused: the Agent generates without streaming.
func (recorder *Recorder) Stream(
	context.Context, []*schema.Message, ...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("agenteval: streaming generation is not recorded")
}

// admit enforces the cap and waits out the minimum gap since the last request.
func (recorder *Recorder) admit(ctx context.Context) (int, error) {
	state := recorder.state
	state.mu.Lock()
	if state.maxRequests > 0 && len(state.requests) >= state.maxRequests {
		state.overBudget = true
		state.mu.Unlock()
		return 0, ErrRequestBudget
	}
	wait := time.Duration(0)
	if !state.lastEnd.IsZero() {
		wait = state.minGap - time.Since(state.lastEnd)
	}
	index := len(state.requests)
	// Reserve the slot so concurrent requests cannot exceed the cap.
	state.requests = append(state.requests, RecordedRequest{Index: index, Turn: state.turn})
	state.mu.Unlock()
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			state.mu.Lock()
			state.requests[index].Error = "canceled"
			state.mu.Unlock()
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
	return index, nil
}

func (recorder *Recorder) finish(record RecordedRequest, err error) {
	state := recorder.state
	state.mu.Lock()
	defer state.mu.Unlock()
	record.Turn = state.requests[record.Index].Turn
	state.requests[record.Index] = record
	state.lastEnd = time.Now()
	if err != nil && !errors.Is(err, context.Canceled) {
		state.providerError = true
	}
}

func toolNames(tools []*schema.ToolInfo) []string {
	names := make([]string, 0, len(tools))
	for _, info := range tools {
		names = append(names, info.Name)
	}
	return names
}

// providerErrorClass keeps only the HTTP status: provider error bodies can
// quote prompts or credentials.
func providerErrorClass(err error) string {
	if apiErr, ok := errors.AsType[*openai.Error](err); ok {
		return "http_" + strconv.Itoa(apiErr.StatusCode)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	return "transport"
}
