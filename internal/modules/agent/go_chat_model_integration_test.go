//go:build integration

package agent //nolint:testpackage // Exercises the provider, real tools, and persisted history.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	dashboardmcp "github.com/mholtzscher/hearth/internal/modules/dashboards/mcpapi"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

const goCallsFixture = `[
 {"index":0,"id":"call_first","type":"function","function":{"name":"echo_text","arguments":"{\"text\":\"first\"}"}},
 {"index":1,"id":"call_second","type":"function","function":{"name":"echo_text","arguments":"{\"text\":\"second\"}"}}
]`

type goChatRequestFixture struct {
	Model    string           `json:"model"`
	Messages []map[string]any `json:"messages"`
	Tools    []struct {
		Type     string `json:"type"`
		Function struct {
			Name       string         `json:"name"`
			Parameters map[string]any `json:"parameters"`
			Strict     *bool          `json:"strict"`
		} `json:"function"`
	} `json:"tools"`
	ReasoningEffort string `json:"reasoning_effort"`
}

//nolint:gocognit // Checks one complete native call/reasoning continuation through a database reopen.
func TestGoContinuationSurvivesRestart(t *testing.T) {
	t.Parallel()
	mcpServer := newBridgeTestServer()
	dashboardmcp.Register(mcpServer, dashboards.NewService(nil, nil, nil))
	requests := make(chan goChatRequestFixture, 3)
	sessions := make(chan string, 3)
	var count atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request goChatRequestFixture
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" ||
			r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("User-Agent") != "hearth/1.0" {
			t.Error("wrong Go request route, credential, or identity")
		}
		requests <- request
		sessions <- r.Header.Get("X-Opencode-Session")
		w.Header().Set("Content-Type", "text/event-stream")
		if count.Add(1) == 1 {
			fmt.Fprint(w, goChatStream("private-call-reasoning", "", goCallsFixture, "tool_calls"))
		} else {
			fmt.Fprint(w, goChatStream("private-final-reasoning", "Both checked.", "", "stop"))
		}
	}))
	defer peer.Close()
	path := filepath.Join(t.TempDir(), "restart.db")
	database := dbtest.OpenMigrated(t, path)
	config := Config{DB: database, Tools: mustBridgeTools(t, mcpServer), Model: ModelConfig{
		Provider:        ProviderOpenCodeGo,
		APIKey:          "test-key",
		BaseURL:         peer.URL + "/v1",
		Model:           DefaultGoModel,
		ReasoningEffort: "max",
	}}
	service, err := NewService(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	conversation := mustCreateConversation(t, service)
	var events []TurnEvent
	turn, err := service.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "Check both"},
		func(event TurnEvent) { events = append(events, event) },
	)
	if err != nil || turn.Reply != "Both checked." || len(turn.ToolCalls) != 2 {
		t.Fatalf("turn=%+v error=%v", turn, err)
	}
	if err = service.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	config.DB = dbtest.OpenMigrated(t, path)
	restarted, err := NewService(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "Continue"}); err != nil {
		t.Fatal(err)
	}
	for step := 1; step <= 3; step++ {
		request := <-requests
		if session := <-sessions; session != conversation.ID {
			t.Fatalf("step %d session=%q", step, session)
		}
		assertGoRequest(t, request)
		if step > 1 {
			assertGoContinuation(t, request.Messages, step == 3)
		}
	}
	public := publicHistoryJSON(t, restarted, conversation.ID)
	eventJSON, _ := json.Marshal(events)
	for _, raw := range []string{string(public), string(eventJSON)} {
		if strings.Contains(raw, "private-") || strings.Contains(raw, goChatMessageKey) ||
			strings.Contains(raw, conversationProviderKey) {
			t.Fatal("private provider data leaked")
		}
	}
}

func assertGoRequest(t *testing.T, request goChatRequestFixture) {
	t.Helper()
	if request.Model != "deepseek-v4.1-flash" || request.ReasoningEffort != "max" {
		t.Fatalf("Go settings: %+v", request)
	}
	found := map[string]bool{}
	for _, info := range request.Tools {
		found[info.Function.Name] = true
		if info.Type != "function" || info.Function.Strict == nil || *info.Function.Strict {
			t.Error("MCP schema strictness changed")
		}
	}
	if !found["skill"] || !found["tool_search"] || found["echo_text"] || found["present_view"] {
		t.Fatalf("unexpected visible tools: %v", found)
	}
}

func assertGoContinuation(t *testing.T, messages []map[string]any, restarted bool) {
	t.Helper()
	var assistants []map[string]any
	results := map[string]string{}
	for _, message := range messages {
		if message["role"] == "assistant" {
			assistants = append(assistants, message)
		}
		if message["role"] == "tool" {
			id, _ := message["tool_call_id"].(string)
			results[id], _ = message["content"].(string)
		}
	}
	want := 1
	if restarted {
		want = 2
	}
	if len(assistants) != want || assistants[0]["reasoning_content"] != "private-call-reasoning" {
		t.Fatalf("lost native reasoning: %v", assistants)
	}
	if restarted && assistants[1]["reasoning_content"] != "private-final-reasoning" {
		t.Fatal("lost final-response reasoning after restart")
	}
	var native goChatAssistant
	raw, _ := json.Marshal(assistants[0])
	if err := json.Unmarshal(
		raw,
		&native,
	); err != nil || len(native.ToolCalls) != 2 || native.ToolCalls[0].ID != "call_first" || native.ToolCalls[1].ID != "call_second" ||
		native.ToolCalls[0].Function.Arguments != `{"text":"first"}` {
		t.Fatalf("native calls changed: %+v error=%v", native, err)
	}
	if !strings.Contains(results["call_first"], "first") || !strings.Contains(results["call_second"], "second") {
		t.Fatalf("lost native result pairing: %v", results)
	}
}

// Only a transient provider status earns the single SDK retry, which happens
// before any response streams. Every failure records a fixed diagnosis and runs
// no tools.
func TestGoFailuresRetryTransientStatusOnceAndNeverExecuteTools(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body, failure string
		status, requests    int
	}{
		{"rate_limit", `{"error":{"message":"private-provider-error"}}`, `"model_failure":"provider_status","provider_status":429`, 429, 2},
		{"unavailable", `{"error":{"message":"private-provider-error"}}`, `"model_failure":"provider_status","provider_status":503`, 503, 2},
		{"rejected", `{"error":{"message":"private-provider-error"}}`, `"model_failure":"provider_status","provider_status":400`, 400, 1},
		{"truncated", "data: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n", `"model_failure":"response_incomplete"`, 200, 1},
		{"length", goChatStream("private", "", goCallsFixture, "length"), `"model_failure":"response_incomplete"`, 200, 1},
		{"filter", goChatStream("private", "", goCallsFixture, "content_filter"), `"model_failure":"response_incomplete"`, 200, 1},
		{"empty", goChatStream("private", "", "", "stop"), `"model_failure":"empty_response"`, 200, 1},
		{"malformed_call", goChatStream("private", "", `[{"index":0,"id":"call_bad","type":"function","function":{"name":"echo_text","arguments":"{"}}]`, "tool_calls"), `"model_failure":"invalid_function_call"`, 200, 1},
		{"duplicate_call", goChatStream("private", "", strings.ReplaceAll(goCallsFixture, "call_second", "call_first"), "tool_calls"), `"model_failure":"invalid_function_call"`, 200, 1},
		{"inconsistent_finish", goChatStream("private", "", goCallsFixture, "stop"), `"model_failure":"inconsistent_tool_completion"`, 200, 1},
		{"stream_error", "data: {\"error\":{\"message\":\"private-provider-error\"}}\n\n", `"model_failure":"provider_stream_error"`, 200, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests, effects atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Retry-After-Ms", "1")
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer peer.Close()
			var logs lockedBuffer
			service := newLoggedGoService(
				t,
				peer.URL,
				[]tool.BaseTool{callbackTool{name: "echo_text", invoke: func(context.Context, string) (string, error) {
					effects.Add(1)
					return "ok", nil
				}}},
				slog.New(slog.NewJSONHandler(&logs, nil)),
			)
			conversation := mustCreateConversation(t, service)
			var events []TurnEvent
			_, err := service.SendMessageWithEvents(
				t.Context(),
				conversation.ID,
				MessageInput{Text: "Act"},
				func(event TurnEvent) { events = append(events, event) },
			)
			var failure modelError
			if !errors.As(err, &failure) || requests.Load() != int32(test.requests) || effects.Load() != 0 {
				t.Fatalf("error=%v requests=%d effects=%d", err, requests.Load(), effects.Load())
			}
			assertFailedTurnEvents(t, events)
			record := logs.String()
			if !strings.Contains(record, test.failure) || strings.Contains(record, "private") {
				t.Fatalf("log record %s lacks %s or leaks provider text", record, test.failure)
			}
		})
	}
}

func TestGoTransientStatusRetrySucceedsWithoutRepeatedWork(t *testing.T) {
	t.Parallel()
	var requests, effects atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch requests.Add(1) {
		case 1:
			w.Header().Set("Retry-After-Ms", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"upstream overloaded"}}`)
		case 2:
			fmt.Fprint(
				w,
				goChatStream(
					"private",
					"",
					`[{"index":0,"id":"call_once","type":"function","function":{"name":"echo_text","arguments":"{}"}}]`,
					"tool_calls",
				),
			)
		default:
			fmt.Fprint(w, goChatStream("private", "Done.", "", "stop"))
		}
	}))
	defer peer.Close()
	service := newGoService(t, peer.URL, []tool.BaseTool{callbackTool{
		name:   "echo_text",
		invoke: func(context.Context, string) (string, error) { effects.Add(1); return "ok", nil },
	}})
	conversation := mustCreateConversation(t, service)
	var preparing int
	turn, err := service.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "Act"},
		func(event TurnEvent) {
			if event.Type == EventToolPreparing {
				preparing++
			}
		},
	)
	if err != nil || turn.Reply != "Done." || requests.Load() != 3 || effects.Load() != 1 || preparing != 1 {
		t.Fatalf(
			"turn=%+v error=%v requests=%d effects=%d preparing=%d",
			turn,
			err,
			requests.Load(),
			effects.Load(),
			preparing,
		)
	}
}

func newGoService(t *testing.T, baseURL string, tools []tool.BaseTool) *Service {
	t.Helper()
	return newLoggedGoService(t, baseURL, tools, nil)
}

func newLoggedGoService(t *testing.T, baseURL string, tools []tool.BaseTool, logger *slog.Logger) *Service {
	t.Helper()
	store := newConversationStore(t, MinimumConversationRetention)
	service, err := NewService(t.Context(), Config{DB: store.database, Tools: tools, Logger: logger, Model: ModelConfig{
		Provider: ProviderOpenCodeGo, APIKey: "test-key", Model: DefaultGoModel, BaseURL: baseURL,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestGoSessionsAreIsolatedAcrossConcurrentConversations(t *testing.T) {
	t.Parallel()
	arrived := make(chan string, 2)
	release := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body goChatRequestFixture
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		last := body.Messages[len(body.Messages)-1]["content"]
		if r.Header.Get("X-Opencode-Session") != last {
			t.Error("conversation header crossed requests")
		}
		arrived <- r.Header.Get("X-Opencode-Session")
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, goChatStream("private", "Ready.", "", "stop"))
	}))
	defer peer.Close()
	defer close(release)
	service := newGoService(t, peer.URL, []tool.BaseTool{stubTool{name: "noop"}})
	conversations := []Conversation{mustCreateConversation(t, service), mustCreateConversation(t, service)}
	finished := make(chan error, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for _, conversation := range conversations {
		go func() {
			_, err := service.SendMessage(ctx, conversation.ID, MessageInput{Text: conversation.ID})
			finished <- err
		}()
	}
	seen := map[string]bool{}
	for range conversations {
		select {
		case id := <-arrived:
			seen[id] = true
		case <-ctx.Done():
			t.Fatal("conversations did not overlap")
		}
	}
	for _, conversation := range conversations {
		if !seen[conversation.ID] {
			t.Fatal("missing distinct session header")
		}
	}
	// One token per blocked handler releases both without closing twice.
	for range conversations {
		release <- struct{}{}
	}
	for range conversations {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
}

func TestGoCancellationJoinsProviderRequest(t *testing.T) {
	t.Parallel()
	started, exited := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		close(started)
		<-r.Context().Done()
		close(exited)
	}))
	defer peer.Close()
	service := newGoService(t, peer.URL, []tool.BaseTool{stubTool{name: "noop"}})
	conversation := mustCreateConversation(t, service)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := service.SendMessage(ctx, conversation.ID, MessageInput{Text: "Wait"})
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not called")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not join")
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("provider still active")
	}
}

func TestProviderSwitchRequiresNewConversation(t *testing.T) {
	t.Parallel()
	service := newTurnService(t, failingChatModel{err: errors.New("must not call model")})
	conversation := mustCreateConversation(t, service)
	// Legacy rows have no provider marker and belong to OpenAI. Retain enough
	// newer rows to prove affinity does not depend on the model's recent window.
	if err := service.persistMessage(t.Context(), conversation.ID, schema.UserMessage("Legacy")); err != nil {
		t.Fatal(err)
	}
	for range historyRowBudget + 1 {
		if err := service.persistMessage(t.Context(), conversation.ID, schema.UserMessage("Recent")); err != nil {
			t.Fatal(err)
		}
	}
	goService, err := NewService(t.Context(), Config{
		DB:    service.database,
		Tools: service.tools,
		Model: ModelConfig{
			Provider: ProviderOpenCodeGo,
		},
		ChatModel: failingChatModel{err: errors.New("must not call model")},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := service.History(t.Context(), conversation.ID)
	var events []TurnEvent
	_, err = goService.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "Continue"},
		func(event TurnEvent) { events = append(events, event) },
	)
	var invalid messageTextError
	if !errors.As(err, &invalid) || invalid.detail != "agent provider changed; start a new conversation" {
		t.Fatalf("provider switch error=%v", err)
	}
	after, _ := service.History(t.Context(), conversation.ID)
	if len(before) != len(after) || len(events) != 1 || events[0].Type != EventTurnFailed ||
		events[0].Error != invalid.detail {
		t.Fatal("provider switch changed history or lacked actionable failure")
	}
	// New Go conversations pin Go even when the model fails before responding.
	fresh := mustCreateConversation(t, goService)
	_, _ = goService.SendMessage(t.Context(), fresh.ID, MessageInput{Text: "First Go turn"})
	_, err = service.SendMessage(t.Context(), fresh.ID, MessageInput{Text: "Back to OpenAI"})
	if !errors.As(err, &invalid) {
		t.Fatalf("reverse switch error=%v", err)
	}
}
