//go:build integration

package agent //nolint:testpackage // Tests inspect turn failure identity and persisted native messages.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestParallelToolFailureRetainsSiblingPersistenceFailure(t *testing.T) {
	t.Parallel()
	calls := []schema.ToolCall{
		nativeTestCall("transport", "broken", `{}`),
		nativeTestCall("persist", "successful", `{}`),
	}
	modelCalls := 0
	script := &scriptedTurnModel{generate: func(
		context.Context, []*schema.Message, ...model.Option,
	) (*schema.Message, error) {
		modelCalls++
		if modelCalls > 1 {
			return schema.AssistantMessage("fabricated final", nil), nil
		}
		return schema.AssistantMessage("", calls), nil
	}}
	service := newTurnService(t, script)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	siblingStarted := make(chan struct{})
	failedFinished := make(chan struct{})
	privateCause := errors.New("private transport failure")
	service.tools = []tool.BaseTool{
		callbackTool{name: "broken", invoke: func(ctx context.Context, _ string) (string, error) {
			select {
			case <-siblingStarted:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return "", toolCallError{err: privateCause}
		}},
		callbackTool{name: "successful", invoke: func(ctx context.Context, _ string) (string, error) {
			close(siblingStarted)
			select {
			case <-failedFinished:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return "completed effect", nil
		}},
	}
	conversation := mustCreateConversation(t, service)
	if _, err := service.database.Exec(`CREATE TRIGGER fail_parallel_result BEFORE INSERT ON agent_messages
		WHEN NEW.role = 'tool' AND json_extract(NEW.message_json, '$.tool_call_id') = 'persist'
		BEGIN SELECT RAISE(ABORT, 'result insert failed'); END`); err != nil {
		t.Fatal(err)
	}
	var events []TurnEvent
	turn, err := service.SendMessageWithEvents(ctx, conversation.ID, "Run both.", func(event TurnEvent) {
		events = append(events, event)
		if event.Type == EventToolFinished && event.Name == "broken" {
			close(failedFinished)
		}
	})
	var persist tracePersistError
	var transport toolCallError
	if !errors.As(err, &persist) || !errors.As(err, &transport) || !errors.Is(err, privateCause) {
		t.Fatalf("error = %v, want both persistence and private transport causes", err)
	}
	if stage := runnerFailureStage(err); stage != turnStagePersist {
		t.Errorf("failure stage = %q, want persistence precedence", stage)
	}
	if modelCalls != 1 || turn.Reply != "" {
		t.Errorf("model calls = %d, turn = %+v, want no final response", modelCalls, turn)
	}
	assertParallelFailedHistory(t, service, conversation.ID)
	if len(events) == 0 || events[len(events)-1].Type != EventTurnFailed {
		t.Errorf("events = %+v, want failed turn", events)
	}
}

func assertParallelFailedHistory(t *testing.T, service *Service, conversationID string) {
	t.Helper()
	history, err := service.loadModelMessages(t.Context(), conversationID)
	if err != nil {
		t.Fatal(err)
	}
	results := nativeToolResults(history)
	if len(history) != 3 || len(results) != 1 || results["transport"] != failedToolOutcome {
		t.Fatalf("history = %+v, want only the failed transport result retained", history)
	}
	if !strings.Contains(strings.ToLower(results["transport"]), "unknown") ||
		!strings.Contains(strings.ToLower(results["transport"]), "do not") ||
		!strings.Contains(strings.ToLower(results["transport"]), "repeat") {
		t.Errorf("failed result does not warn against repeating an unknown outcome: %q", results["transport"])
	}
}

func TestParallelResultPersistsWhileFinishedCallbackBlocked(t *testing.T) {
	t.Parallel()
	calls := []schema.ToolCall{
		nativeTestCall("first-id", "first", `{}`),
		nativeTestCall("second-id", "second", `{}`),
	}
	modelCalls := 0
	script := &scriptedTurnModel{generate: func(
		context.Context, []*schema.Message, ...model.Option,
	) (*schema.Message, error) {
		modelCalls++
		if modelCalls == 1 {
			return schema.AssistantMessage("", calls), nil
		}
		return schema.AssistantMessage("Done.", nil), nil
	}}
	service := newTurnService(t, script)
	ctx, cancel := context.WithCancel(t.Context())
	secondStarted := make(chan struct{})
	firstFinished := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	service.tools = []tool.BaseTool{
		callbackTool{name: "first", invoke: func(ctx context.Context, _ string) (string, error) {
			select {
			case <-secondStarted:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return "first result", nil
		}},
		callbackTool{name: "second", invoke: func(ctx context.Context, _ string) (string, error) {
			close(secondStarted)
			select {
			case <-firstFinished:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return "second result", nil
		}},
	}
	conversation := mustCreateConversation(t, service)
	var events []TurnEvent
	var turn Turn
	var turnErr error
	exited := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Error("turn did not join during cleanup")
		}
	})
	go func() {
		defer close(exited)
		turn, turnErr = service.SendMessageWithEvents(ctx, conversation.ID, "Run both.", func(event TurnEvent) {
			events = append(events, event)
			if event.Type == EventToolFinished && event.Name == "first" {
				close(firstFinished)
				<-release
			}
		})
	}()
	select {
	case <-firstFinished:
	case <-time.After(5 * time.Second):
		t.Fatal("first finished callback did not start")
	}
	waitForSecondParallelResult(t, service, conversation.ID)
	select {
	case <-exited:
		t.Fatal("turn returned while its callback was blocked")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish after callback release")
	}
	if turnErr != nil || turn.Reply != "Done." || modelCalls != 2 {
		t.Fatalf("turn = %+v, error = %v, model calls = %d", turn, turnErr, modelCalls)
	}
	assertParallelToolEventOrder(t, events)
	history, err := service.loadModelMessages(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	results := nativeToolResults(history)
	if len(history) != 5 || results["first-id"] != "first result" || results["second-id"] != "second result" {
		t.Errorf("history = %+v, want both results and final", history)
	}
}

func waitForSecondParallelResult(t *testing.T, service *Service, conversationID string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		err := service.database.QueryRowContext(t.Context(), `SELECT count(*) FROM agent_messages
			WHERE conversation_id = ? AND role = 'tool' AND json_extract(message_json, '$.tool_call_id') = 'second-id'`,
			conversationID).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("second result was not persisted while the first callback was blocked")
		case <-ticker.C:
		}
	}
}

func assertParallelToolEventOrder(t *testing.T, events []TurnEvent) {
	t.Helper()
	if len(events) != 6 || events[0].Type != EventTurnStarted || events[len(events)-1].Type != EventTurnFinished {
		t.Fatalf("events = %+v", events)
	}
	positions := map[string]int{}
	for i, event := range events {
		if event.Type == EventToolStarted || event.Type == EventToolFinished {
			key := event.Type + ":" + event.Name
			if _, exists := positions[key]; exists {
				t.Fatalf("duplicate tool event: %+v", events)
			}
			positions[key] = i
		}
	}
	for _, name := range []string{"first", "second"} {
		started, hasStart := positions[EventToolStarted+":"+name]
		finished, hasFinish := positions[EventToolFinished+":"+name]
		if !hasStart || !hasFinish || started >= finished {
			t.Errorf("events for %s out of order: %+v", name, events)
		}
	}
}
