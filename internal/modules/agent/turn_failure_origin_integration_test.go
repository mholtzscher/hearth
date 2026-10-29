//go:build integration

package agent //nolint:testpackage // Tests drive real turns and inspect private error identity.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTurnFailuresRetainOrigin(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{"provider", "tool_transport", "tool_protocol", "trace_persistence", "empty_response", "graph_setup", "graph_limit"} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			service, script, logs := newFailureOriginService(t, origin)
			conversation := mustCreateConversation(t, service)
			var events []TurnEvent
			_, err := service.SendMessageWithEvents(
				t.Context(),
				conversation.ID,
				"synthetic request",
				func(event TurnEvent) {
					events = append(events, event)
				},
			)
			if err == nil {
				t.Fatal("faulted turn succeeded")
			}
			assertFailureOrigin(t, script, err)
			assertOriginDiagnostic(t, logs.String(), origin, conversation.ID)
			assertFailedTurnTrace(t, service, conversation.ID, origin, events)
			rawEvents, marshalErr := json.Marshal(events)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if strings.Contains(logs.String()+string(rawEvents), "synthetic-private-cause") {
				t.Error("diagnostic or turn event leaked the synthetic private cause")
			}
		})
	}
}

func assertFailureOrigin(t *testing.T, script *failureOriginModel, err error) {
	t.Helper()
	var provider modelError
	wantModel := script.origin == "provider" || script.origin == "empty_response"
	if isModel := errors.As(err, &provider); isModel != wantModel {
		t.Errorf("model error = %t, want %t", isModel, wantModel)
	}
	wantCalls := 1
	if script.origin == "graph_setup" {
		wantCalls = 0
	}
	if script.calls != wantCalls {
		t.Errorf("model calls = %d, want %d", script.calls, wantCalls)
	}
	if (script.origin == "provider" || script.origin == "graph_setup") && !errors.Is(err, script.failure) {
		t.Error("private cause lost through the graph")
	}
	if script.origin == "tool_protocol" {
		var protocolErr *jsonrpc.Error
		if !errors.As(err, &protocolErr) || protocolErr.Code != -32603 ||
			protocolErr.Message != "synthetic-private-cause" {
			t.Error("private protocol cause lost through the graph")
		}
	}
}

func assertOriginDiagnostic(t *testing.T, raw, origin, conversationID string) {
	t.Helper()
	var record struct {
		Event          string `json:"event"`
		Stage          string `json:"stage"`
		Code           string `json:"error_code"`
		ConversationID string `json:"conversation_id"`
	}
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatalf("want exactly one JSON diagnostic: %v", err)
	}
	wantStage, wantCode := "graph", "turn_failed"
	switch origin {
	case "provider":
		wantStage, wantCode = "model", "model_call_failed"
	case "empty_response":
		wantStage, wantCode = "model", "model_response_invalid"
	case "tool_transport", "tool_protocol":
		wantStage, wantCode = "tool", "tool_call_failed"
	case "trace_persistence":
		wantStage, wantCode = "persist", "trace_persist_failed"
	}
	if record.Event != "agent.turn_failed" || record.Stage != wantStage || record.Code != wantCode ||
		record.ConversationID != conversationID {
		t.Fatalf("diagnostic = %+v, want %s/%s for this conversation", record, wantStage, wantCode)
	}
}

func assertFailedTurnTrace(t *testing.T, service *Service, conversationID, origin string, events []TurnEvent) {
	t.Helper()
	if len(events) < 2 || events[0].Type != EventTurnStarted || events[len(events)-1].Type != EventTurnFailed {
		t.Fatalf("events = %+v, want a started and failed turn", events)
	}
	if events[len(events)-1].Error != "agent turn failed" {
		t.Fatalf("terminal text = %q, want opaque failure", events[len(events)-1].Error)
	}
	history, err := service.History(t.Context(), conversationID)
	if err != nil {
		t.Fatal(err)
	}
	wantRows := 1
	if origin == "tool_transport" || origin == "tool_protocol" {
		wantRows = 3 // User plus the atomic failed-tool call/result pair.
	}
	if len(history) != wantRows || history[0].Role != string(schema.User) {
		t.Fatalf("history = %+v, want %d rows with the user first", history, wantRows)
	}
	if wantRows == 3 && (history[1].Role != string(schema.Assistant) || history[2].Role != string(schema.Tool) ||
		history[2].Content != "error: agent tool call failed") {
		t.Fatalf("failed-tool history = %+v, want a paired call and safe failure result", history)
	}
}

func TestTurnFailuresHTTPContract(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{"provider", "tool_transport", "tool_protocol", "trace_persistence", "empty_response", "graph_setup", "graph_limit"} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			service, _, logs := newFailureOriginService(t, origin)
			conversation := mustCreateConversation(t, service)
			router := newOperationsRouterWithLogger(service, service.logger)
			request := httptest.NewRequest(http.MethodPost, "/v1/agent/conversations/"+conversation.ID+"/messages",
				strings.NewReader(`{"text":"synthetic request"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			var problem struct {
				Status int    `json:"status"`
				Title  string `json:"title"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			wantStatus, wantDetail := http.StatusInternalServerError, "internal error"
			if origin == "provider" || origin == "empty_response" {
				wantStatus, wantDetail = http.StatusBadGateway, "agent model call failed"
			}
			if response.Code != wantStatus || problem.Status != wantStatus || problem.Detail != wantDetail ||
				problem.Title != http.StatusText(wantStatus) {
				t.Fatalf("status=%d problem=%+v, want %d/%s", response.Code, problem, wantStatus, wantDetail)
			}
			if response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("content type = %q, want a Huma problem", response.Header().Get("Content-Type"))
			}
			if strings.Contains(response.Body.String()+logs.String(), "synthetic-private-cause") {
				t.Fatal("response or diagnostic leaked the synthetic private cause")
			}
			assertTurnDiagnosticOwner(t, logs.String())
		})
	}
}

func TestTurnFailuresStreamContract(t *testing.T) {
	t.Parallel()
	for _, origin := range []string{"provider", "tool_protocol", "trace_persistence", "empty_response"} {
		t.Run(origin, func(t *testing.T) {
			t.Parallel()
			service, _, logs := newFailureOriginService(t, origin)
			conversation := mustCreateConversation(t, service)
			router := newOperationsRouterWithLogger(service, service.logger)
			RegisterHTTPStream(router, service, service.logger)
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/agent/conversations/"+conversation.ID+"/messages/stream",
				strings.NewReader(`{"text":"synthetic request"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" {
				t.Fatalf("response = %d %s, want a committed SSE stream", response.Code, response.Header())
			}
			events := parseStreamEvents(t, response.Body.String())
			assertFailedTurnTrace(t, service, conversation.ID, origin, events)
			wantTypes := []string{EventTurnStarted, EventTurnFailed}
			switch origin {
			case "tool_protocol":
				wantTypes = []string{EventTurnStarted, EventToolStarted, EventToolFinished, EventTurnFailed}
			case "trace_persistence":
				wantTypes = []string{EventTurnStarted, EventToolStarted, EventTurnFailed}
			}
			var types []string
			for _, event := range events {
				types = append(types, event.Type)
			}
			if !reflect.DeepEqual(types, wantTypes) {
				t.Fatalf("event types = %v, want %v", types, wantTypes)
			}
			if strings.Contains(response.Body.String()+logs.String(), "synthetic-private-cause") {
				t.Fatal("stream or diagnostic leaked the private cause")
			}
			assertTurnDiagnosticOwner(t, logs.String())
		})
	}
}

func assertTurnDiagnosticOwner(t *testing.T, raw string) {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatalf("want exactly one turn diagnostic: %v", err)
	}
	if record["event"] != "agent.turn_failed" {
		t.Fatalf("diagnostic owner=%v, want turn service", record)
	}
}

func TestTurnCallerCancellationPreservesCause(t *testing.T) {
	t.Parallel()
	chatModel := newFakeChatModel("unused")
	service := newTurnService(t, chatModel)
	conversation := mustCreateConversation(t, service)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := service.SendMessage(ctx, conversation.ID, "synthetic request")
		done <- err
	}()
	select {
	case <-chatModel.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn never reached model")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.As(err, &turnCancellationError{}) {
			t.Fatalf("turn error = %v, want preserved caller cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not honor caller cancellation")
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Role != string(schema.User) {
		t.Fatalf("history = %+v, want only the admitted user message", history)
	}
}

func newFailureOriginService(t *testing.T, origin string) (*Service, *failureOriginModel, *lockedBuffer) {
	t.Helper()
	store := newConversationStore(t, MinimumConversationRetention)
	script := &failureOriginModel{origin: origin, failure: errors.New("synthetic-private-cause")}
	tools := []tool.BaseTool{stubTool{name: "noop"}}
	switch origin {
	case "tool_transport":
		tools = []tool.BaseTool{closedSessionTool(t)}
	case "tool_protocol":
		server := newBridgeTestServer()
		server.Raw().AddTool(&mcp.Tool{Name: "echo_text", InputSchema: map[string]any{"type": "object"}},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return nil, &jsonrpc.Error{Code: -32603, Message: "synthetic-private-cause"}
			})
		tools = mustBridgeTools(t, server)
	case "trace_persistence":
		tools = mustBridgeTools(t, newBridgeTestServer())
		// Fail the second row of the two-row statement, after a valid user write
		// and successful tool call, without making later history reads fail.
		if _, err := store.database.Exec(`CREATE TRIGGER fail_tool_trace BEFORE INSERT ON agent_messages
			WHEN NEW.role = 'tool' BEGIN SELECT RAISE(ABORT, 'synthetic-private-cause'); END`); err != nil {
			t.Fatal(err)
		}
	case "graph_limit":
		tools = mustBridgeTools(t, newBridgeTestServer())
	}
	logs := &lockedBuffer{}
	service, err := NewService(t.Context(), Config{
		DB: store.database, Tools: tools, ChatModel: script,
		Retention: MinimumConversationRetention,
		Logger:    slog.New(slog.NewJSONHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if origin == "graph_limit" {
		service.maxSteps = 1
	}
	return service, script, logs
}

func closedSessionTool(t *testing.T) tool.BaseTool {
	t.Helper()
	server := newBridgeTestServer()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Raw().Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "failure-origin-test"}, nil).
		Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	bridged, err := mcpTool(&mcp.Tool{Name: "echo_text", InputSchema: map[string]any{"type": "object"}}, session)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	return bridged
}

type failureOriginModel struct {
	failingChatModel

	origin  string
	failure error
	calls   int
}

func (script *failureOriginModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	if script.origin == "graph_setup" {
		return nil, script.failure
	}
	return script, nil
}

func (script *failureOriginModel) Generate(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.Message, error) {
	script.calls++
	switch script.origin {
	case "provider":
		return nil, script.failure
	case "empty_response":
		return nil, nil //nolint:nilnil // Deliberate malformed provider response.
	default:
		if script.calls > 1 {
			return schema.AssistantMessage("unexpected recovery", nil), nil
		}
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "synthetic-call", Type: "function",
			Function: schema.FunctionCall{Name: "echo_text", Arguments: `{"text":"synthetic argument"}`},
		}}), nil
	}
}
