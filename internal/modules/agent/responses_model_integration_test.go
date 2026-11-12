//go:build integration

package agent //nolint:testpackage // Exercises the real provider adapter and persisted conversation.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	dashboardmcp "github.com/mholtzscher/hearth/internal/modules/dashboards/mcpapi"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
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
		writeResponsesStream(w, fmt.Sprintf(
			`{"id":"resp_%d","object":"response","status":"completed","output":%s,"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`,
			step,
			output,
		))
	}))
	defer server.Close()
	store := newConversationStore(t, MinimumConversationRetention)
	mcpServer := newBridgeTestServer()
	dashboardmcp.Register(mcpServer, dashboards.NewService(nil, nil, nil))
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

// TestResponsesDashboardPresentation uses a synthetic Responses peer, the real
// dashboard MCP registration and Eino bridge, and the actual SQLite history.
//
//nolint:cyclop,gocognit,gocyclo // Checks one complete protocol, persistence, and restart journey.
func TestResponsesDashboardPresentation(t *testing.T) {
	t.Parallel()
	mcpServer := dashboardMCPServer(dashboards.NewService(nil, nil, nil))
	definition := dashboardDefinitionFixture(t, "Large draft", true)
	arguments, err := json.Marshal(map[string]json.RawMessage{"definition": definition})
	if err != nil {
		t.Fatal(err)
	}
	calls, err := json.Marshal([]any{
		map[string]any{
			"id":                "rs_view",
			"type":              "reasoning",
			"summary":           []any{},
			"encrypted_content": "private-view-reasoning",
		},
		map[string]any{
			"id":        "fc_view",
			"type":      "function_call",
			"call_id":   "native_view",
			"name":      "present_view",
			"arguments": string(arguments),
			"status":    "completed",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	inputs := make(chan responsesRequestFixture, 3)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request responsesRequestFixture
		if decodeErr := json.NewDecoder(r.Body).Decode(&request); decodeErr != nil {
			t.Error(decodeErr)
			return
		}
		inputs <- request
		step := requests.Add(1)
		output := responsesFinalFixture
		if step == 1 {
			output = string(calls)
		}
		writeResponsesStream(w, fmt.Sprintf(
			`{"id":"resp_%d","object":"response","status":"completed","output":%s,"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}`,
			step,
			output,
		))
	}))
	defer peer.Close()
	path := filepath.Join(t.TempDir(), "dashboard-restart.db")
	database := dbtest.OpenMigrated(t, path)
	config := Config{
		DB:    database,
		Tools: mustBridgeTools(t, mcpServer),
		Model: ModelConfig{APIKey: "test-key", BaseURL: peer.URL + "/v1", Model: "fixture"},
	}
	service, err := NewService(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	conversation := mustCreateConversation(t, service)
	contextDefinition, err := dashboards.ParseDefinition(dashboardDefinitionFixture(t, "Selected draft", false))
	if err != nil {
		t.Fatal(err)
	}
	sourceID := dashboards.DashboardID("dash_018f0f4a-7b29-7000-8000-000000000001")
	revision := int64(3)
	var events []TurnEvent
	turn, err := service.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		MessageInput{
			Text: "Generate",
			ViewContext: &ViewContext{
				Definition:        contextDefinition,
				SourceDashboardID: &sourceID,
				SourceRevision:    &revision,
			},
		},
		func(event TurnEvent) {
			events = append(events, event)
			if event.Type == EventViewPresented {
				var count int
				queryErr := service.database.QueryRowContext(t.Context(), `SELECT count(*) FROM agent_messages WHERE conversation_id = ? AND role = 'tool' AND json_extract(message_json, '$.tool_call_id') = ?`, conversation.ID, event.CallID).
					Scan(&count)
				if queryErr != nil || count != 1 {
					t.Errorf("view delivered before commit: count=%d err=%v", count, queryErr)
				}
			}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.Artifacts) != 1 || len(turn.Artifacts[0].Definition) <= maxEventResult {
		t.Fatalf("turn artifacts=%+v", turn.Artifacts)
	}
	// The view is announced while the model writes it, before it runs.
	if len(events) != 6 || events[1].Type != EventToolPreparing || events[1].CallID != "native_view" ||
		events[1].Name != "present_view" || events[1].Arguments != "" ||
		events[2].Type != EventToolStarted || events[2].CallID != "native_view" ||
		events[3].Type != EventToolFinished ||
		events[3].CallID != "native_view" ||
		!strings.Contains(events[3].Result, "[truncated]") ||
		events[4].Type != EventViewPresented ||
		events[4].CallID != "native_view" ||
		events[4].Artifact == nil ||
		len(events[4].Artifact.Definition) <= maxEventResult {
		t.Fatalf("events=%+v", events)
	}
	if err = service.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	config.DB = dbtest.OpenMigrated(t, path)
	config.Tools = mustBridgeTools(t, dashboardMCPServer(dashboards.NewService(nil, nil, nil)))
	restarted, err := NewService(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	history, err := restarted.History(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 || !strings.Contains(string(history[0].ViewContext), string(sourceID)) ||
		!strings.Contains(string(history[0].ViewContext), `"source_revision":3`) ||
		len(history[1].Calls) != 1 ||
		history[1].Calls[0].ID != "native_view" ||
		history[1].Calls[0].Arguments != string(arguments) ||
		history[2].ToolCallID != "native_view" ||
		len(history[2].Artifacts) != 1 ||
		history[2].Artifacts[0].ArtifactID != turn.Artifacts[0].ArtifactID ||
		!reflect.DeepEqual(history[2].Artifacts[0].Definition, turn.Artifacts[0].Definition) {
		t.Fatalf("history=%+v", history)
	}
	public, err := json.Marshal(struct {
		History json.RawMessage `json:"history"`
		Events  []TurnEvent     `json:"events"`
		Turn    TurnBody        `json:"turn"`
	}{publicHistoryJSON(t, restarted, conversation.ID), events, TurnBody(turn)})
	if err != nil || strings.Contains(string(public), "private-view-reasoning") ||
		strings.Contains(string(public), responsesOutputKey) {
		t.Fatal("private provider output leaked")
	}
	if _, err = restarted.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "Continue"}); err != nil {
		t.Fatal(err)
	}
	for step := 1; step <= 3; step++ {
		request := <-inputs
		assertDashboardResponsesRequest(t, request, calls, turn.Artifacts[0].Artifact, step)
	}
}

func assertDashboardResponsesRequest(
	t *testing.T,
	request responsesRequestFixture,
	callOutput []byte,
	artifact dashboards.Artifact,
	step int,
) {
	t.Helper()
	found := false
	for _, info := range request.Tools {
		if info["name"] == "present_view" {
			found = true
		}
	}
	if found {
		t.Error("unselected dashboard tool was visible")
	}
	raw, err := json.Marshal(request.Input)
	if err != nil || strings.Count(string(raw), "Selected view draft") != 1 ||
		!strings.Contains(string(raw), "Selected draft") {
		t.Errorf("step %d missing or repeated selected context", step)
	}
	if step > 1 &&
		(!strings.Contains(string(raw), "private-view-reasoning") || !strings.Contains(string(raw), "native_view")) {
		t.Errorf("step %d lost private continuation or call ID", step)
	}
	if step > 1 {
		assertDashboardContinuation(t, request.Input, callOutput, artifact, step)
	}
}

func assertDashboardContinuation(
	t *testing.T,
	input []map[string]any,
	callOutput []byte,
	artifact dashboards.Artifact,
	step int,
) {
	t.Helper()
	var expected []map[string]any
	if err := json.Unmarshal(callOutput, &expected); err != nil {
		t.Fatal(err)
	}
	var retained []map[string]any
	var output string
	for _, item := range input {
		if item["id"] == "rs_view" || item["id"] == "fc_view" {
			retained = append(retained, item)
		}
		if item["type"] == "function_call_output" && item["call_id"] == "native_view" {
			output, _ = item["output"].(string)
		}
	}
	if !reflect.DeepEqual(retained, expected) {
		t.Errorf("step %d provider items changed: %v", step, retained)
	}
	projected, err := dashboards.ParseArtifact([]byte(output))
	if err != nil || projected.ArtifactID != artifact.ArtifactID ||
		string(projected.Definition) != string(artifact.Definition) {
		t.Errorf("step %d continuation result mismatch: %v", step, err)
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
	}
	if !found["skill"] || !found["tool_search"] || found["echo_text"] || found["present_view"] {
		t.Errorf("unexpected visible tools: %v", found)
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

// Only a transient provider status earns the single SDK retry, which happens
// before any response streams. Every failure records a fixed diagnosis and runs
// no tools.
func TestResponsesFailuresRetryTransientStatusOnceAndNeverExecuteTools(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body, failure string
		status, requests    int
	}{
		{"rate_limit", `{"error":{"message":"private-provider-error","type":"rate_limit_error"}}`, `"model_failure":"provider_status","provider_status":429`, 429, 2},
		{"unavailable", `{"error":{"message":"private-provider-error","type":"server_error"}}`, `"model_failure":"provider_status","provider_status":503`, 503, 2},
		{"incomplete", responsesStream(`{"status":"incomplete","output":` + responsesCallsFixture + `}`), `"model_failure":"response_incomplete"`, 200, 1},
		{"failed", responsesStream(`{"status":"failed","error":{"message":"private-provider-error"},"output":[]}`), `"model_failure":"response_incomplete"`, 200, 1},
		{"empty", responsesStream(`{"status":"completed","output":[]}`), `"model_failure":"empty_response"`, 200, 1},
		{"stream_error", "event: error\ndata: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"private-provider-error\"}\n\n", `"model_failure":"provider_stream_error"`, 200, 1},
		{"truncated", "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\",\"output\":[]}}\n\n", `"model_failure":"response_incomplete"`, 200, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests, effects atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				contentType := "application/json"
				if test.status == http.StatusOK {
					contentType = "text/event-stream"
				}
				w.Header().Set("Content-Type", contentType)
				w.Header().Set("Retry-After-Ms", "1")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			store := newConversationStore(t, MinimumConversationRetention)
			var logs lockedBuffer
			service, err := NewService(
				t.Context(),
				Config{
					DB:     store.database,
					Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
					Model:  ModelConfig{APIKey: "test-key", BaseURL: server.URL, Model: "fixture"},
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

// assertFailedTurnEvents checks a turn that failed at the provider: calls the
// model announced before its response failed never start, and the private
// provider error stays out of the public failure.
func assertFailedTurnEvents(t *testing.T, events []TurnEvent) {
	t.Helper()
	last := events[len(events)-1]
	if events[0].Type != EventTurnStarted || last.Type != EventTurnFailed ||
		strings.Contains(last.Error, "private-provider-error") {
		t.Fatalf("events=%+v", events)
	}
	for _, event := range events[1 : len(events)-1] {
		if event.Type != EventToolPreparing {
			t.Fatalf("unexpected event before failure: %+v", event)
		}
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
