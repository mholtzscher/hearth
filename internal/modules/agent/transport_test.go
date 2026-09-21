package agent //nolint:testpackage // Tests stub the narrow Operations seam.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
)

// stubOperations is a test double for the narrow registration surface, so
// transport mapping can be exercised without a database or a chat model.
type stubOperations struct {
	createConversation func(ctx context.Context) (Conversation, error)
	listConversations  func(ctx context.Context) ([]ConversationSummary, error)
	sendMessage        func(ctx context.Context, conversationID, text string) (Turn, error)
	sendMessageEvents  func(ctx context.Context, conversationID, text string, emit func(TurnEvent)) (Turn, error)
	history            func(ctx context.Context, conversationID string) ([]StoredMessage, error)
	conversationExists func(ctx context.Context, conversationID string) (bool, error)
	admissionOpen      func() bool
}

func (stub *stubOperations) CreateConversation(ctx context.Context) (Conversation, error) {
	return stub.createConversation(ctx)
}

func (stub *stubOperations) ListConversations(ctx context.Context) ([]ConversationSummary, error) {
	return stub.listConversations(ctx)
}

func (stub *stubOperations) SendMessage(ctx context.Context, conversationID, text string) (Turn, error) {
	return stub.sendMessage(ctx, conversationID, text)
}

func (stub *stubOperations) SendMessageWithEvents(
	ctx context.Context,
	conversationID, text string,
	emit func(TurnEvent),
) (Turn, error) {
	return stub.sendMessageEvents(ctx, conversationID, text, emit)
}

func (stub *stubOperations) History(ctx context.Context, conversationID string) ([]StoredMessage, error) {
	return stub.history(ctx, conversationID)
}

func (stub *stubOperations) ConversationExists(ctx context.Context, conversationID string) (bool, error) {
	return stub.conversationExists(ctx, conversationID)
}

func (stub *stubOperations) AdmissionOpen() bool {
	return stub.admissionOpen()
}

func newOperationsRouter(service Operations) *echo.Echo {
	router := echo.New()
	openapi := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	Register(huma.NewGroup(openapi, "/v1"), service)
	return router
}

func TestSendMessageRouteMapsAdmissionAndCancellation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantDetail string
	}{
		{
			name:       "closed admission",
			err:        ErrAdmissionUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantDetail: "agent admission is unavailable",
		},
		{
			name:       "cancelled turn",
			err:        fmt.Errorf("agent turn: %w", context.Canceled),
			wantStatus: http.StatusServiceUnavailable,
			wantDetail: "agent turn canceled",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			service := &stubOperations{
				sendMessage: func(context.Context, string, string) (Turn, error) {
					return Turn{}, testCase.err
				},
			}
			router := newOperationsRouter(service)
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/agent/conversations/aconv_test/messages",
				strings.NewReader(`{"text":"hello"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), testCase.wantDetail) {
				t.Fatalf("body = %s, want detail %q", recorder.Body.String(), testCase.wantDetail)
			}
		})
	}
}

func TestSendMessageRouteMapsInvalidTextToBadRequest(t *testing.T) {
	t.Parallel()
	service := &stubOperations{
		sendMessage: func(context.Context, string, string) (Turn, error) {
			return Turn{}, messageTextError{detail: "agent message text is required"}
		},
	}
	router := newOperationsRouter(service)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/agent/conversations/aconv_test/messages",
		strings.NewReader(`{"text":"   "}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "agent message text is required") {
		t.Fatalf("body = %s, want the validation detail", recorder.Body.String())
	}
}

func TestStreamRouteRejectsClosedAdmission(t *testing.T) {
	t.Parallel()
	service := &stubOperations{admissionOpen: func() bool { return false }}
	router := echo.New()
	RegisterStream(router, service)

	recorder := postStream(t, router)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "agent admission is unavailable") {
		t.Fatalf("body = %s, want the admission detail", recorder.Body.String())
	}
}

func TestStreamRouteRejectsUnknownConversation(t *testing.T) {
	t.Parallel()
	service := &stubOperations{
		admissionOpen: func() bool { return true },
		conversationExists: func(context.Context, string) (bool, error) {
			return false, nil
		},
	}
	router := echo.New()
	RegisterStream(router, service)

	recorder := postStream(t, router)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
}

func TestStreamRouteForwardsTurnEvents(t *testing.T) {
	t.Parallel()
	service := &stubOperations{
		admissionOpen: func() bool { return true },
		conversationExists: func(context.Context, string) (bool, error) {
			return true, nil
		},
		sendMessageEvents: func(
			_ context.Context, _, _ string, emit func(TurnEvent),
		) (Turn, error) {
			emit(TurnEvent{Type: EventTurnStarted})
			emit(TurnEvent{Type: EventTurnFinished, Reply: "hi"})
			return Turn{Reply: "hi"}, nil
		},
	}
	router := echo.New()
	RegisterStream(router, service)

	recorder := postStream(t, router)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	// nginx buffers proxied responses unless the response opts out, which would
	// hold every event until the turn ended.
	if buffering := recorder.Header().Get("X-Accel-Buffering"); buffering != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", buffering)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: "+EventTurnFinished) {
		t.Fatalf("body = %s, want the turn.finished event", body)
	}
	if !strings.Contains(body, `"reply":"hi"`) {
		t.Fatalf("body = %s, want the reply payload", body)
	}
}

func postStream(t *testing.T, router *echo.Echo) *httptest.ResponseRecorder {
	t.Helper()
	return postStreamBody(t, router, `{"text":"hello"}`)
}

// postStreamBody posts one raw JSON body to the stream route.
func postStreamBody(t *testing.T, router *echo.Echo, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/agent/conversations/aconv_test/messages/stream",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}
