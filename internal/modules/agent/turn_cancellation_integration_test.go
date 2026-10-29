//go:build integration

package agent //nolint:testpackage // Tests exercise turn admission and the real service.

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
)

func TestTurnOwnedCancellationIsNotAnInternalFailure(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ stop, position string }{
		{"caller", "active"}, {"caller", "queued"}, {"drain", "active"}, {"drain", "queued"},
	} {
		stop, position := test.stop, test.position
		t.Run(stop+"/"+position, func(t *testing.T) {
			t.Parallel()
			chatModel := newFakeChatModel("unused")
			service := newTurnService(t, chatModel)
			conversation := mustCreateConversation(t, service)
			var logs lockedBuffer
			service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			if position == "queued" {
				assertQueuedCancellation(t, service, conversation.ID, stop)
			} else {
				assertActiveCancellation(t, service, conversation.ID, chatModel, stop)
			}
			record := logs.String()
			if record != "" {
				t.Errorf("expected caller/shutdown cancellation emitted a failure diagnostic: %s", record)
			}
			history, err := service.History(t.Context(), conversation.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantRows := 1
			if position == "queued" {
				wantRows = 0
			}
			if len(history) != wantRows {
				t.Fatalf("history rows = %d, want %d", len(history), wantRows)
			}
		})
	}
}

func assertActiveCancellation(t *testing.T, service *Service, id string, chatModel *fakeChatModel, stop string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := service.SendMessage(ctx, id, "synthetic request")
		done <- err
	}()
	select {
	case <-chatModel.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn never reached model")
	}
	if stop == "drain" {
		if err := service.Drain(t.Context()); err != nil {
			t.Fatal(err)
		}
	} else {
		cancel()
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.As(err, &turnCancellationError{}) {
			t.Fatalf("turn error = %v, want cancellation identity", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn failed to stop")
	}
}

func assertQueuedCancellation(t *testing.T, service *Service, id, stop string) {
	t.Helper()
	// The held gate prevents database/model work. Wait makes the admitted turn
	// deterministically block on that gate before caller cancellation or Drain.
	synctest.Test(t, func(t *testing.T) {
		release, err := service.acquireConversationTurn(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, sendErr := service.SendMessage(ctx, id, "synthetic queued request")
			done <- sendErr
		}()
		synctest.Wait()
		if stop == "drain" {
			if drainErr := service.Drain(t.Context()); drainErr != nil {
				t.Fatal(drainErr)
			}
		} else {
			cancel()
		}
		if sendErr := <-done; !errors.Is(sendErr, context.Canceled) || !errors.As(sendErr, &turnCancellationError{}) {
			t.Fatalf("queued turn error = %v, want cancellation identity", sendErr)
		}
	})
}

func TestTurnDependencyContextFailuresKeepTheirOrigin(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		cause     error
		transport string
	}{
		{context.Canceled, "http"}, {context.Canceled, "sse"},
		{context.DeadlineExceeded, "http"}, {context.DeadlineExceeded, "sse"},
	} {
		cause, transport := test.cause, test.transport
		t.Run(cause.Error()+"/"+transport, func(t *testing.T) {
			t.Parallel()
			service, script, logs := newFailureOriginService(t, "provider")
			script.failure = cause
			conversation := mustCreateConversation(t, service)
			router := newOperationsRouter(service)
			path := "/v1/agent/conversations/" + conversation.ID + "/messages"
			if transport == "sse" {
				RegisterHTTPStream(router, service, nil)
				path += "/stream"
			}
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path,
				strings.NewReader(`{"text":"synthetic request"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if request.Context().Err() != nil {
				t.Fatal("fixture canceled the owning request")
			}
			assertDependencyContextResponse(t, response, transport)
			var record struct {
				Code string `json:"error_code"`
			}
			if err := json.Unmarshal([]byte(logs.String()), &record); err != nil {
				t.Fatal(err)
			}
			if record.Code != "model_call_failed" {
				t.Errorf("diagnostic code = %q, want provider origin", record.Code)
			}
		})
	}
}

func assertDependencyContextResponse(t *testing.T, response *httptest.ResponseRecorder, transport string) {
	t.Helper()
	if transport == "http" {
		var problem struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusBadGateway || problem.Detail != "agent model call failed" {
			t.Fatalf("status=%d problem=%+v, want provider failure", response.Code, problem)
		}
		return
	}
	events := parseStreamEvents(t, response.Body.String())
	if response.Code != http.StatusOK || len(events) != 2 || events[1].Type != EventTurnFailed ||
		events[1].Error != "agent turn failed" {
		t.Fatalf("status=%d events=%+v, want opaque failure after turn.started", response.Code, events)
	}
}

func TestTurnCallerDeadlineKeepsCancellationIdentity(t *testing.T) {
	t.Parallel()
	service := newTurnService(t, failingChatModel{err: errors.New("model not installed")})
	conversation := mustCreateConversation(t, service)
	var logs lockedBuffer
	service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	synctest.Test(t, func(t *testing.T) {
		// Both model wait channels must belong to the bubble for fake time to
		// advance while Generate waits for the caller deadline.
		chatModel := newFakeChatModel("unused")
		service.chatModel = chatModel
		ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := service.SendMessage(ctx, conversation.ID, "synthetic request")
			done <- err
		}()
		<-chatModel.started
		time.Sleep(time.Hour)
		err := <-done
		if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &turnCancellationError{}) {
			t.Fatalf("turn error = %v, want owned deadline", err)
		}
	})
	if logs.String() != "" {
		t.Fatalf("caller deadline emitted failure diagnostic: %s", logs.String())
	}
}

func TestTurnOwnedCancellationTransportContract(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ stop, transport string }{
		{"caller", "http"}, {"caller", "sse"}, {"drain", "http"}, {"drain", "sse"},
	} {
		t.Run(test.stop+"/"+test.transport, func(t *testing.T) {
			t.Parallel()
			chatModel := newFakeChatModel("unused")
			service := newTurnService(t, chatModel)
			conversation := mustCreateConversation(t, service)
			var logs lockedBuffer
			service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			router := newOperationsRouter(service)
			path := "/v1/agent/conversations/" + conversation.ID + "/messages"
			if test.transport == "sse" {
				RegisterHTTPStream(router, service, nil)
				path += "/stream"
			}
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, path,
				strings.NewReader(`{"text":"synthetic request"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				router.ServeHTTP(response, request)
				close(done)
			}()
			select {
			case <-chatModel.started:
			case <-time.After(5 * time.Second):
				t.Fatal("transport turn never reached model")
			}
			if test.stop == "drain" {
				if err := service.Drain(t.Context()); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			<-done
			assertOwnedCancellationResponse(t, response, test.transport)
			if logs.String() != "" {
				t.Fatalf("expected cancellation emitted failure diagnostic: %s", logs.String())
			}
		})
	}
}

func assertOwnedCancellationResponse(t *testing.T, response *httptest.ResponseRecorder, transport string) {
	t.Helper()
	if transport == "http" {
		var problem struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusServiceUnavailable || problem.Detail != "agent turn canceled" {
			t.Fatalf("status=%d problem=%+v, want existing cancellation contract", response.Code, problem)
		}
		return
	}
	events := parseStreamEvents(t, response.Body.String())
	if response.Code != http.StatusOK || len(events) != 2 || events[1].Type != EventTurnFailed ||
		events[1].Error != "agent turn canceled" {
		t.Fatalf("status=%d events=%+v, want cancellation after turn.started", response.Code, events)
	}
}
