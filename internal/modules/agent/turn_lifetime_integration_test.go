//go:build integration

package agent //nolint:testpackage // Tests exercise the turn lifetime of the real service.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cloudwego/eino/schema"
)

func requireRunning(t *testing.T, service *Service, conversationID string) {
	t.Helper()
	state, ok := service.TurnState(conversationID)
	if !ok || state.State != TurnStateRunning || state.StartedAt.IsZero() {
		t.Fatalf("turn state = %+v, %v, want running", state, ok)
	}
}

// This test fails if a caller leaving cancels its turn, if the caller is not
// told the turn continues, or if the finished turn leaves its reply missing or
// its running state behind.
func TestTurnContinuesAfterCallerLeaves(t *testing.T) {
	t.Parallel()
	service := newTurnService(t, failingChatModel{err: errors.New("replaced in the bubble")})
	conversation := mustCreateConversation(t, service)
	var logs lockedBuffer
	service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	synctest.Test(t, func(t *testing.T) {
		chatModel := newFakeChatModel("Both sensors have been unavailable since startup.")
		service.chatModel = chatModel
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			_, err := service.SendMessage(ctx, conversation.ID, MessageInput{Text: "Which sensors are offline?"})
			done <- err
		}()
		<-chatModel.started
		requireRunning(t, service, conversation.ID)
		cancel()
		err := <-done
		if !errors.As(err, &TurnContinuesError{}) || !errors.Is(err, context.Canceled) {
			t.Fatalf("caller error = %v, want the turn to continue", err)
		}
		requireRunning(t, service, conversation.ID)
		close(chatModel.release)
		synctest.Wait()
	})
	if state, ok := service.TurnState(conversation.ID); ok {
		t.Fatalf("finished turn left state %+v", state)
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[1].Role != string(schema.Assistant) ||
		history[1].Content != "Both sensors have been unavailable since startup." {
		t.Fatalf("history = %+v, want the user message and the reply", history)
	}
	if logs.String() != "" {
		t.Fatalf("a caller leaving emitted a diagnostic: %s", logs.String())
	}
}

// This test fails if either transport cancels the turn when its client
// disconnects, or if history stops reporting the running turn to a client
// that comes back.
func TestTurnSurvivesTransportDisconnect(t *testing.T) {
	t.Parallel()
	for _, transport := range []string{"http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			service := newTurnService(t, failingChatModel{err: errors.New("replaced in the bubble")})
			conversation := mustCreateConversation(t, service)
			router := newOperationsRouter(service)
			path := "/v1/agent/conversations/" + conversation.ID + "/messages"
			historyPath := path
			if transport == "sse" {
				RegisterHTTPStream(router, service, nil)
				path += "/stream"
			}
			synctest.Test(t, func(t *testing.T) {
				chatModel := newFakeChatModel("Done.")
				service.chatModel = chatModel
				ctx, cancel := context.WithCancel(t.Context())
				request := httptest.NewRequestWithContext(ctx, http.MethodPost, path,
					strings.NewReader(`{"text":"synthetic request"}`))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				served := make(chan struct{})
				go func() {
					router.ServeHTTP(response, request)
					close(served)
				}()
				<-chatModel.started
				cancel()
				<-served
				assertDisconnectResponse(t, response, transport)
				if turn := readHistoryTurn(t, router, historyPath); turn == nil || turn.State != TurnStateRunning {
					t.Fatalf("history turn = %+v, want running", turn)
				}
				close(chatModel.release)
				synctest.Wait()
			})
			if turn := readHistoryTurn(t, router, historyPath); turn != nil {
				t.Fatalf("history turn after finishing = %+v, want none", turn)
			}
		})
	}
}

func assertDisconnectResponse(t *testing.T, response *httptest.ResponseRecorder, transport string) {
	t.Helper()
	if transport == "http" {
		var problem struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusServiceUnavailable ||
			problem.Detail != "agent turn continues in the background" {
			t.Fatalf("status=%d problem=%+v", response.Code, problem)
		}
		return
	}
	events := parseStreamEvents(t, response.Body.String())
	if response.Code != http.StatusOK || len(events) != 1 || events[0].Type != EventTurnStarted {
		t.Fatalf("status=%d events=%+v, want only turn.started before the client left", response.Code, events)
	}
}

func readHistoryTurn(t *testing.T, router http.Handler, path string) *TurnStateBody {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("history status=%d body=%s", response.Code, response.Body.String())
	}
	var body HistoryBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Turn
}

// This test fails if a turn that outlives its own deadline is treated as a
// silent cancellation instead of a logged, reported failure.
func TestTurnDeadlineIsAReportedFailure(t *testing.T) {
	t.Parallel()
	service := newTurnService(t, failingChatModel{err: errors.New("model not installed")})
	conversation := mustCreateConversation(t, service)
	var logs lockedBuffer
	service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	service.turnTimeout = time.Hour
	synctest.Test(t, func(t *testing.T) {
		// The model's wait channels must belong to the bubble for fake time to
		// advance while Generate waits.
		chatModel := newFakeChatModel("unused")
		service.chatModel = chatModel
		done := make(chan error, 1)
		go func() {
			_, err := service.SendMessage(
				context.Background(),
				conversation.ID,
				MessageInput{Text: "synthetic request"},
			)
			done <- err
		}()
		<-chatModel.started
		time.Sleep(time.Hour)
		err := <-done
		if !errors.As(err, &turnDeadlineError{}) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("turn error = %v, want the turn deadline", err)
		}
	})
	var record map[string]any
	if err := json.Unmarshal([]byte(logs.String()), &record); err != nil {
		t.Fatalf("want exactly one turn diagnostic, got %q: %v", logs.String(), err)
	}
	if record["event"] != "agent.turn_failed" || record["stage"] != turnStageDeadline ||
		record["error_code"] != "turn_deadline_exceeded" {
		t.Fatalf("diagnostic = %v", record)
	}
	state, ok := service.TurnState(conversation.ID)
	if !ok || state.State != TurnStateFailed || state.Error != "agent turn timed out" {
		t.Fatalf("turn state = %+v, %v, want failed with the timeout text", state, ok)
	}
}

// This test fails if a failed turn is not reported to a returning client, or
// if the next turn does not replace that report.
func TestTurnStateReportsFailureUntilTheNextTurn(t *testing.T) {
	t.Parallel()
	service := newTurnService(t, failingChatModel{err: errors.New("synthetic provider failure")})
	conversation := mustCreateConversation(t, service)
	service.logger = slog.New(slog.DiscardHandler)
	if _, err := service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "first"}); err == nil {
		t.Fatal("failing model produced a reply")
	}
	state, ok := service.TurnState(conversation.ID)
	if !ok || state.State != TurnStateFailed || state.Error != "agent turn failed" || state.Prompt != "first" {
		t.Fatalf("turn state = %+v, %v, want failed with its own prompt", state, ok)
	}
	chatModel := newFakeChatModel("Recovered.")
	close(chatModel.release)
	service.chatModel = chatModel
	if _, err := service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "second"}); err != nil {
		t.Fatal(err)
	}
	if state, ok = service.TurnState(conversation.ID); ok {
		t.Fatalf("turn state after a finished turn = %+v", state)
	}
}

// This test fails if a turn for a conversation that does not exist records a
// state, which would let arbitrary IDs grow the state map.
func TestTurnForUnknownConversationRecordsNoState(t *testing.T) {
	t.Parallel()
	service := newTurnService(t, failingChatModel{err: errors.New("must not call model")})
	service.logger = slog.New(slog.DiscardHandler)
	const missing = "aconv_00000000000000000000000000000000"
	if _, err := service.SendMessage(
		t.Context(),
		missing,
		MessageInput{Text: "hello"},
	); !errors.Is(
		err,
		ErrConversationNotFound,
	) {
		t.Fatalf("err = %v, want not found", err)
	}
	if state, ok := service.TurnState(missing); ok {
		t.Fatalf("unknown conversation recorded %+v", state)
	}
}
