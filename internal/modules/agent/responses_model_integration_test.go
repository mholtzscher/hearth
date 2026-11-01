//go:build integration

package agent //nolint:testpackage // Exercises the real provider adapter and persisted conversation.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	dashboardmcp "github.com/mholtzscher/hearth/internal/modules/dashboards/mcpapi"
)

const responsesCallsFixture = `[
 {"id":"rs_calls","type":"reasoning","summary":[],"encrypted_content":"opaque-call-reasoning"},
 {"id":"fc_first","type":"function_call","call_id":"call_first","name":"echo_text","arguments":"{\"text\":\"first\"}","status":"completed"},
 {"id":"fc_second","type":"function_call","call_id":"call_second","name":"echo_text","arguments":"{\"text\":\"second\"}","status":"completed"}
]`

const responsesFinalFixture = `[
 {"id":"rs_final","type":"reasoning","summary":[],"encrypted_content":"opaque-final-reasoning"},
 {"id":"msg_final","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Both checked.","annotations":[]}]}
]`

//nolint:gocognit // Covers one complete provider continuation across restart.
func TestResponsesToolsAndReasoningSurviveHistoryReload(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request responsesRequestFixture
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		assertResponsesRequest(t, r, request)
		step := requests.Add(1)
		encodedInput, err := json.Marshal(request.Input)
		if err != nil || strings.Count(string(encodedInput), "Selected view draft") != 1 ||
			!strings.Contains(string(encodedInput), "Draft title") {
			t.Errorf("selected draft not expanded exactly once in step %d", step)
		}
		if step > 1 {
			assertResponsesContinuation(t, request.Input, step == 3)
		}
		output := responsesFinalFixture
		if step == 1 {
			output = responsesCallsFixture
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(
			w,
			`{"id":"resp_%d","object":"response","status":"completed","output":%s,"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`,
			step,
			output,
		)
	}))
	defer server.Close()
	store := newConversationStore(t, MinimumConversationRetention)
	mcpServer := newBridgeTestServer()
	dashboardmcp.Register(mcpServer, dashboards.NewService(nil, nil))
	config := Config{DB: store.database, Tools: mustBridgeTools(t, mcpServer), Model: ModelConfig{
		APIKey: "test-key", BaseURL: server.URL + "/v1", Model: "reasoning-fixture", ReasoningEffort: "medium",
	}}
	service, err := NewService(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	conversation := mustCreateConversation(t, service)
	definition, err := dashboards.ParseDefinition(
		[]byte(
			`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Draft title","spec":{"root":"note","elements":{"note":{"type":"Note","props":{"text":"Hello"}}}}}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.SendMessage(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "Check both.", ViewContext: &ViewContext{Definition: definition}},
	)
	if err != nil || turn.Reply != "Both checked." || len(turn.ToolCalls) != 2 {
		t.Fatalf("turn=%+v err=%v", turn, err)
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range history {
		if strings.Contains(message.Content, "opaque-") {
			t.Fatal("provider reasoning leaked into public history")
		}
	}
	if len(history) == 0 || !strings.Contains(string(history[0].ViewContext), "Draft title") ||
		history[0].Content != "Check both." {
		t.Fatalf("context history: %+v", history)
	}
	// Reconstruct the provider and service over the same SQLite data. Continuation
	// must use retained items, not an SDK cache or server-side response chain.
	restarted, err := NewService(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.SendMessage(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "What did you find?"},
	); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("provider requests=%d, want 3", got)
	}
}

type responsesRequestFixture struct {
	Model     string           `json:"model"`
	Input     []map[string]any `json:"input"`
	Tools     []map[string]any `json:"tools"`
	Reasoning struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
	Store              *bool    `json:"store"`
	Include            []string `json:"include"`
	PreviousResponseID string   `json:"previous_response_id"`
}

func assertResponsesRequest(t *testing.T, r *http.Request, request responsesRequestFixture) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" ||
		r.Header.Get("Authorization") != "Bearer test-key" {
		t.Errorf("provider request method=%s path=%s or credential mismatch", r.Method, r.URL.Path)
	}
	if request.Model != "reasoning-fixture" || request.Reasoning.Effort != "medium" || request.Store == nil ||
		*request.Store ||
		request.PreviousResponseID != "" {
		t.Errorf("wrong provider configuration: %+v", request)
	}
	if !reflect.DeepEqual(request.Include, []string{"reasoning.encrypted_content"}) {
		t.Errorf("include=%v", request.Include)
	}
	found := map[string]bool{}
	for _, info := range request.Tools {
		name, _ := info["name"].(string)
		found[name] = true
		if info["type"] != "function" || info["strict"] != false {
			t.Errorf("tool changed MCP schema mode: %v", info)
		}
		if name == "present_view" {
			raw, err := json.Marshal(info["parameters"])
			if err != nil || !strings.Contains(string(raw), "schema_version") ||
				!strings.Contains(string(raw), "Metric") ||
				!strings.Contains(string(raw), "additionalProperties") {
				t.Errorf("incomplete dashboard schema: %s (%v)", raw, err)
			}
		}
	}
	if !found["skill"] || !found["echo_text"] || !found["present_view"] {
		t.Errorf("missing native or MCP tools: %v", found)
	}
}

func assertResponsesContinuation(t *testing.T, input []map[string]any, restarted bool) {
	t.Helper()
	var expected []map[string]any
	if err := json.Unmarshal([]byte(responsesCallsFixture), &expected); err != nil {
		t.Fatal(err)
	}
	if restarted {
		var final []map[string]any
		if err := json.Unmarshal([]byte(responsesFinalFixture), &final); err != nil {
			t.Fatal(err)
		}
		expected = append(expected, final...)
	}
	var retained []map[string]any
	results := map[string]string{}
	for _, item := range input {
		switch item["type"] {
		case "reasoning", "function_call":
			retained = append(retained, item)
		case "message":
			if item["role"] == "assistant" {
				retained = append(retained, item)
			}
		case "function_call_output":
			id, _ := item["call_id"].(string)
			text, _ := item["output"].(string)
			results[id] = text
		}
	}
	if !reflect.DeepEqual(retained, expected) {
		t.Errorf("retained provider items=%v, want %v", retained, expected)
	}
	if !strings.Contains(results["call_first"], "first") || !strings.Contains(results["call_second"], "second") {
		t.Errorf("native tool result pairing=%v", results)
	}
}

func TestResponsesFailuresDoNotRetryOrExecuteTools(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"rate_limit", 429, `{"error":{"message":"private-provider-error","type":"rate_limit_error"}}`},
		{"unavailable", 503, `{"error":{"message":"private-provider-error","type":"server_error"}}`},
		{"incomplete", 200, `{"status":"incomplete","output":` + responsesCallsFixture + `}`},
		{"failed", 200, `{"status":"failed","error":{"message":"private-provider-error"},"output":[]}`},
		{"empty", 200, `{"status":"completed","output":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests, effects atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			store := newConversationStore(t, MinimumConversationRetention)
			service, err := NewService(
				t.Context(),
				Config{
					DB:    store.database,
					Model: ModelConfig{APIKey: "test-key", BaseURL: server.URL, Model: "fixture"},
					Tools: []tool.BaseTool{
						callbackTool{
							name:   "echo_text",
							invoke: func(context.Context, string) (string, error) { effects.Add(1); return "ok", nil },
						},
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			conversation := mustCreateConversation(t, service)
			var events []TurnEvent
			_, err = service.SendMessageWithEvents(
				t.Context(),
				conversation.ID,
				MessageInput{Text: "Act."},
				func(event TurnEvent) { events = append(events, event) },
			)
			var failure modelError
			if !errors.As(err, &failure) || requests.Load() != 1 || effects.Load() != 0 {
				t.Fatalf("error=%v requests=%d effects=%d", err, requests.Load(), effects.Load())
			}
			if len(events) != 2 || events[1].Type != EventTurnFailed ||
				strings.Contains(events[1].Error, "private-provider-error") {
				t.Fatalf("events=%+v", events)
			}
		})
	}
}

func TestResponsesRequestJoinsOnCallerCancellation(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	exited := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		close(started)
		<-r.Context().Done()
		close(exited)
	}))
	defer server.Close()
	store := newConversationStore(t, MinimumConversationRetention)
	service, err := NewService(
		t.Context(),
		Config{
			DB:    store.database,
			Model: ModelConfig{APIKey: "test-key", BaseURL: server.URL, Model: "fixture"},
			Tools: []tool.BaseTool{stubTool{name: "noop"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	conversation := mustCreateConversation(t, service)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, sendErr := service.SendMessage(ctx, conversation.ID, MessageInput{Text: "Wait."})
		done <- sendErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider request did not start")
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn failed to join")
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("provider request still active")
	}
}
