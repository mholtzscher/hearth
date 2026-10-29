package agent //nolint:testpackage // Tests use the real HTTP/SSE handlers over Operations.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentHTTPDiagnosticsOwnConversationAndPreflightFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, method, path, body, operation, stage string }{
		{"create", "POST", "/v1/agent/conversations", "", "create-agent-conversation", "service"},
		{"list", "GET", "/v1/agent/conversations", "", "list-agent-conversations", "service"},
		{"history", "GET", "/v1/agent/conversations/aconv_synthetic/messages", "", "list-agent-messages", "service"},
		{"SSE preflight", "POST", "/v1/agent/conversations/aconv_synthetic/messages/stream", `{"text":"synthetic"}`, "send-agent-message-stream", "preflight"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			cause := errors.New("SENSITIVE-agent-storage")
			stub := &stubOperations{
				createConversation: func(context.Context) (Conversation, error) { return Conversation{}, cause },
				listConversations:  func(context.Context) ([]ConversationSummary, error) { return nil, cause },
				history:            func(context.Context, string) ([]StoredMessage, error) { return nil, cause },
				conversationExists: func(context.Context, string) (bool, error) { return false, cause },
				admissionOpen:      func() bool { return true },
			}
			router := newOperationsRouterWithLogger(stub, logger)
			RegisterHTTPStream(router, stub, logger)
			request := httptest.NewRequestWithContext(t.Context(), test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != 500 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String()+fmt.Sprint(response.Header())+logs.String(), "SENSITIVE") {
				t.Fatal("private cause leaked")
			}
			var record map[string]any
			if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
				t.Errorf("want exactly one safe diagnostic: %v", err)
			} else if record["level"] != "ERROR" || record["event"] != "agent.http_internal_failure" ||
				record["operation"] != test.operation || record["stage"] != test.stage || record["error_code"] != "internal_error" ||
				record["error_type"] == "" || len(record) != 8 {
				t.Errorf("unexpected diagnostic fields: %v", record)
			}
		})
	}
}
