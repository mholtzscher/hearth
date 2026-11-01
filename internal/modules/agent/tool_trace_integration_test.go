//go:build integration

package agent //nolint:testpackage // Tests verify native ADK messages against SQLite.

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestNativeParallelCallsRetainGroupingAndResults(t *testing.T) {
	t.Parallel()
	calls := []schema.ToolCall{
		nativeTestCall("native-a", "noop", `{"value":1}`),
		nativeTestCall("native-b", "noop", `{"value":2}`),
	}
	step := 0
	script := &scriptedTurnModel{
		generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			step++
			if step == 1 {
				return schema.AssistantMessage("Checking both.", calls), nil
			}
			assertNativePairs(t, messages, calls)
			return schema.AssistantMessage("Done.", nil), nil
		},
	}
	service := newTurnService(t, script)
	service.tools = []tool.BaseTool{
		callbackTool{name: "noop", invoke: func(_ context.Context, args string) (string, error) { return args, nil }},
	}
	conversation := mustCreateConversation(t, service)
	var events []TurnEvent
	turn, err := service.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		"Check both.",
		func(event TurnEvent) { events = append(events, event) },
	)
	if err != nil || turn.Reply != "Done." || len(turn.ToolCalls) != 2 {
		t.Fatalf("turn = %+v, error = %v", turn, err)
	}
	history, err := service.loadModelMessages(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 5 || history[1].Content != "Checking both." || !reflect.DeepEqual(history[1].ToolCalls, calls) {
		t.Fatalf("history = %+v, want user, one native batch, two results and final", history)
	}
	assertNativePairs(t, history, calls)
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Type]++
	}
	if counts[EventToolStarted] != 2 || counts[EventToolFinished] != 2 || counts[EventTurnFinished] != 1 {
		t.Fatalf("events = %+v", events)
	}
	if _, err = service.SendMessage(t.Context(), conversation.ID, "Remember those results."); err != nil {
		t.Fatal(err)
	}
}

func TestPartialParallelBatchRepairsOnlyModelInput(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	released := make(chan struct{})
	slowStarted := make(chan struct{})
	toolExited := make(chan struct{})
	calls := []schema.ToolCall{nativeTestCall("completed", "fast", `{}`), nativeTestCall("unfinished", "slow", `{}`)}
	step := 0
	script := &scriptedTurnModel{
		generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			step++
			if step == 1 {
				return schema.AssistantMessage("", calls), nil
			}
			results := nativeToolResults(messages)
			if results["completed"] != "retained" || results["unfinished"] != unknownToolOutcome {
				t.Errorf("next model results = %v", results)
			}
			return schema.AssistantMessage("The second outcome is unknown.", nil), nil
		},
	}
	service := newTurnService(t, script)
	service.tools = []tool.BaseTool{
		callbackTool{
			name:   "fast",
			invoke: func(context.Context, string) (string, error) { <-slowStarted; return "retained", nil },
		},
		callbackTool{name: "slow", invoke: func(ctx context.Context, _ string) (string, error) {
			close(slowStarted)
			<-ctx.Done()
			<-released
			close(toolExited)
			return "", ctx.Err()
		}},
	}
	conversation := mustCreateConversation(t, service)
	done := make(chan error, 1)
	go func() {
		_, err := service.SendMessageWithEvents(ctx, conversation.ID, "Run both.", func(event TurnEvent) {
			if event.Type == EventToolFinished && event.Name == "fast" {
				cancel()
			}
		})
		done <- err
	}()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("first result not retained")
	}
	select {
	case err := <-done:
		t.Fatalf("turn returned before tool joined: %v", err)
	default:
	}
	close(released)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("turn error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not join")
	}
	select {
	case <-toolExited:
	default:
		t.Fatal("tool still running")
	}
	history, err := service.loadModelMessages(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || !reflect.DeepEqual(history[1].ToolCalls, calls) || history[2].Content != "retained" {
		t.Fatalf("partial history = %+v", history)
	}
	if _, err = service.SendMessage(t.Context(), conversation.ID, "What happened?"); err != nil {
		t.Fatal(err)
	}
	assertNoPersistedRepair(t, service, conversation.ID)
	if step != 2 {
		t.Fatalf("model calls = %d, want no replay", step)
	}
}

func TestAssistantPersistenceFailurePreventsToolExecution(t *testing.T) {
	t.Parallel()
	script := &scriptedTurnModel{
		generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
			return schema.AssistantMessage("", []schema.ToolCall{nativeTestCall("effect", "noop", `{}`)}), nil
		},
	}
	service := newTurnService(t, script)
	invoked := false
	service.tools = []tool.BaseTool{
		callbackTool{
			name:   "noop",
			invoke: func(context.Context, string) (string, error) { invoked = true; return "ok", nil },
		},
	}
	conversation := mustCreateConversation(t, service)
	if _, err := service.database.Exec(
		`CREATE TRIGGER fail_assistant BEFORE INSERT ON agent_messages WHEN NEW.role = 'assistant' BEGIN SELECT RAISE(ABORT, 'failed'); END`,
	); err != nil {
		t.Fatal(err)
	}
	_, err := service.SendMessage(t.Context(), conversation.ID, "Act.")
	var persist tracePersistError
	if !errors.As(err, &persist) || invoked {
		t.Fatalf("error = %v, invoked = %t", err, invoked)
	}
}

func TestNativeIterationLimit(t *testing.T) {
	t.Parallel()
	for _, limit := range []int{0, 1, 3} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			t.Parallel()
			calls, tools := 0, 0
			script := &scriptedTurnModel{
				generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
					calls++
					return schema.AssistantMessage(
						"",
						[]schema.ToolCall{nativeTestCall(strconv.Itoa(calls), "noop", `{}`)},
					), nil
				},
			}
			store := newConversationStore(t, MinimumConversationRetention)
			service, err := NewService(
				t.Context(),
				Config{DB: store.database, ChatModel: script, MaxIterations: limit, Tools: []tool.BaseTool{
					callbackTool{
						name:   "noop",
						invoke: func(context.Context, string) (string, error) { tools++; return "ok", nil },
					},
				}},
			)
			if err != nil {
				t.Fatal(err)
			}
			conversation := mustCreateConversation(t, service)
			_, err = service.SendMessage(t.Context(), conversation.ID, "Keep calling.")
			want := limit
			if want == 0 {
				want = 10
			}
			if !errors.Is(err, adk.ErrExceedMaxIterations) || calls != want || tools != want {
				t.Fatalf("calls=%d tools=%d error=%v, want %d cycles", calls, tools, err, want)
			}
		})
	}
}

func TestLostToolResponseRetainsUnknownEffectOutcome(t *testing.T) {
	t.Parallel()
	effects, generations := 0, 0
	script := &scriptedTurnModel{
		generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			generations++
			if generations == 1 {
				return schema.AssistantMessage("", []schema.ToolCall{nativeTestCall("write", "save", `{}`)}), nil
			}
			if got := nativeToolResults(messages)["write"]; got != failedToolOutcome {
				t.Errorf("lost response observation = %q, want explicit unknown outcome", got)
			}
			return schema.AssistantMessage("The save outcome is unknown; inspect it before retrying.", nil), nil
		},
	}
	service := newTurnService(t, script)
	service.tools = []tool.BaseTool{callbackTool{name: "save", invoke: func(context.Context, string) (string, error) {
		effects++
		return "", toolCallError{err: errors.New("response lost after save")}
	}}}
	conversation := mustCreateConversation(t, service)
	if _, err := service.SendMessage(t.Context(), conversation.ID, "Save."); err == nil {
		t.Fatal("lost response did not fail turn")
	}
	if _, err := service.SendMessage(t.Context(), conversation.ID, "What happened?"); err != nil {
		t.Fatal(err)
	}
	if effects != 1 || generations != 2 {
		t.Fatalf("effects=%d generations=%d, want no automatic replay", effects, generations)
	}
}

func assertNativePairs(t *testing.T, messages []*schema.Message, calls []schema.ToolCall) {
	t.Helper()
	results := nativeToolResults(messages)
	for _, call := range calls {
		if results[call.ID] != call.Function.Arguments {
			t.Errorf("result %s = %q", call.ID, results[call.ID])
		}
	}
}

func nativeToolResults(messages []*schema.Message) map[string]string {
	results := map[string]string{}
	for _, message := range messages {
		if message.Role == schema.Tool {
			results[message.ToolCallID] = message.Content
		}
	}
	return results
}

func assertNoPersistedRepair(t *testing.T, service *Service, conversationID string) {
	t.Helper()
	history, err := service.loadModelMessages(t.Context(), conversationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range history {
		if message.Content == unknownToolOutcome {
			t.Fatal("input repair became persisted execution evidence")
		}
	}
}
