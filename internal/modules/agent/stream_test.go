package agent //nolint:testpackage // Tests stub the narrow Operations seam.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/labstack/echo/v5"
)

// TestStreamRouteRejectsUnusableMessageText protects the SSE route's input
// contract: it has no Huma schema, so an over-long message must be rejected here
// instead of being persisted and sent to the model. It fails if the route keeps
// the old blank-only check.
func TestStreamRouteRejectsUnusableMessageText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		text        string
		wantDetail  string
		wantReached bool
	}{
		{name: "blank", text: "   ", wantDetail: "agent message text is required"},
		{
			name:       "over the rune limit",
			text:       strings.Repeat("a", maxMessageRunes+1),
			wantDetail: fmt.Sprintf("agent message text must be at most %d characters", maxMessageRunes),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			reached := false
			service := &stubOperations{
				admissionOpen: func() bool { return true },
				conversationExists: func(context.Context, string) (bool, error) {
					return true, nil
				},
				sendMessageEvents: func(
					context.Context, string, string, func(TurnEvent),
				) (Turn, error) {
					reached = true
					return Turn{}, nil
				},
			}
			router := echo.New()
			RegisterStream(router, service)

			body, marshalErr := json.Marshal(streamRequest{Text: testCase.text})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			recorder := postStreamBody(t, router, string(body))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), testCase.wantDetail) {
				t.Fatalf("body = %s, want detail %q", recorder.Body.String(), testCase.wantDetail)
			}
			if reached {
				t.Fatal("the turn ran despite unusable message text")
			}
		})
	}
}

// TestStreamRouteAcceptsTheRunLimitBoundary protects the rune-based bound: a
// message at exactly maxMessageRunes is valid, and one character over is not, so
// the limit cannot silently drift to bytes.
func TestStreamRouteAcceptsTheRunLimitBoundary(t *testing.T) {
	t.Parallel()
	reached := false
	service := &stubOperations{
		admissionOpen: func() bool { return true },
		conversationExists: func(context.Context, string) (bool, error) {
			return true, nil
		},
		sendMessageEvents: func(
			context.Context, string, string, func(TurnEvent),
		) (Turn, error) {
			reached = true
			return Turn{Reply: "ok"}, nil
		},
	}
	router := echo.New()
	RegisterStream(router, service)

	// Accented characters exceed the limit in bytes while staying inside it in
	// characters.
	text := strings.Repeat("é", maxMessageRunes)
	body, marshalErr := json.Marshal(streamRequest{Text: text})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	recorder := postStreamBody(t, router, string(body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !reached {
		t.Fatal("a message at the character limit never reached the turn")
	}
}

// TestStreamRouteBoundsTheRequestedBody protects the body bound: the JSON
// serializer reads the whole body before decoding, so an unbounded request would
// allocate arbitrary memory. It fails if the route decodes the body unbounded.
func TestStreamRouteBoundsTheRequestedBody(t *testing.T) {
	t.Parallel()
	service := &stubOperations{
		admissionOpen: func() bool { return true },
		conversationExists: func(context.Context, string) (bool, error) {
			return true, nil
		},
		sendMessageEvents: func(
			context.Context, string, string, func(TurnEvent),
		) (Turn, error) {
			t.Error("an oversized body reached the turn")
			return Turn{}, nil
		},
	}
	router := echo.New()
	RegisterStream(router, service)

	oversized := `{"text":"` + strings.Repeat("a", maxStreamBodyBytes) + `"}`
	recorder := postStreamBody(t, router, oversized)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"status = %d, want %d: %s",
			recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String(),
		)
	}
	if !strings.Contains(recorder.Body.String(), "request body is too large") {
		t.Fatalf("body = %s, want the body-bound detail", recorder.Body.String())
	}
}

// TestStreamRouteSerializesConcurrentEmit protects SSE frame integrity: a model
// step with several tool calls emits from graph goroutines, and
// [http.ResponseWriter] is not safe for concurrent use. It fails if two emitters
// can interleave a marshaled frame with a write.
func TestStreamRouteSerializesConcurrentEmit(t *testing.T) {
	t.Parallel()
	const emitters = 8
	const eventsPerEmitter = 16
	service := &stubOperations{
		admissionOpen: func() bool { return true },
		conversationExists: func(context.Context, string) (bool, error) {
			return true, nil
		},
		sendMessageEvents: func(
			_ context.Context, _, _ string, emit func(TurnEvent),
		) (Turn, error) {
			start := make(chan struct{})
			var writers sync.WaitGroup
			for emitter := range emitters {
				writers.Add(1)
				go func(emitter int) {
					defer writers.Done()
					<-start
					for index := range eventsPerEmitter {
						emit(TurnEvent{
							Type: EventToolStarted,
							Name: fmt.Sprintf("tool-%d-%d", emitter, index),
						})
					}
				}(emitter)
			}
			close(start)
			writers.Wait()
			return Turn{Reply: "done"}, nil
		},
	}
	router := echo.New()
	RegisterStream(router, service)

	recorder := postStream(t, router)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	events := parseStreamEvents(t, recorder.Body.String())
	if len(events) != emitters*eventsPerEmitter {
		t.Fatalf("frames = %d, want %d intact frames", len(events), emitters*eventsPerEmitter)
	}
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		if event.Type != EventToolStarted || !strings.HasPrefix(event.Name, "tool-") {
			t.Fatalf("event = %+v, want one intact tool.started frame", event)
		}
		if seen[event.Name] {
			t.Fatalf("event %q appeared twice", event.Name)
		}
		seen[event.Name] = true
	}
}

// parseStreamEvents decodes every SSE frame, failing on any frame whose data
// line is not exactly one JSON event.
func parseStreamEvents(t *testing.T, body string) []TurnEvent {
	t.Helper()
	var events []TurnEvent
	for frame := range strings.SplitSeq(body, "\n\n") {
		if strings.TrimSpace(frame) == "" {
			continue
		}
		lines := strings.Split(frame, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("malformed SSE frame: %q", frame)
		}
		var event TurnEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &event); err != nil {
			t.Fatalf("frame %q does not hold one JSON event: %v", frame, err)
		}
		events = append(events, event)
	}
	return events
}
