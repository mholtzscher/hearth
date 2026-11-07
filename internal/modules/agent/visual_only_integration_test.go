//go:build integration

package agent //nolint:testpackage // Exercises both native providers, real presentation tools, and durable turns.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

// Synthetic provider streams prove the completion contract, not model selection quality.
func TestVisualOnlyCompletion(t *testing.T) {
	t.Parallel()
	for _, provider := range []Provider{ProviderOpenCodeGo, ProviderOpenAI} {
		t.Run(string(provider), func(t *testing.T) {
			t.Parallel()
			testVisualOnlyCompletion(t, provider)
		})
	}
}

func testVisualOnlyCompletion(t *testing.T, provider Provider) {
	t.Helper()
	definition := dashboardDefinitionFixture(t, "Temperatures", false)
	arguments, err := json.Marshal(map[string]json.RawMessage{"definition": definition})
	if err != nil {
		t.Fatal(err)
	}
	peer, requests := newVisualOnlyPeer(t, provider, arguments)
	defer peer.Close()
	store := newConversationStore(t, MinimumConversationRetention)
	service, err := NewService(t.Context(), Config{
		DB: store.database, Tools: mustBridgeTools(t, dashboardMCPServer(dashboards.NewService(nil, nil, nil))),
		Model: ModelConfig{Provider: provider, APIKey: "test-key", Model: "fixture", BaseURL: peer.URL + "/v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation := mustCreateConversation(t, service)
	var events []TurnEvent
	turn, err := service.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "Show temperatures"},
		func(event TurnEvent) { events = append(events, event) },
	)
	if err != nil {
		t.Fatalf("visual-only turn failed: %v", err)
	}
	if turn.Reply != "" || len(turn.Artifacts) != 1 || requests.Load() != 2 {
		t.Fatalf("turn=%+v requests=%d", turn, requests.Load())
	}
	terminal := events[len(events)-1]
	if terminal.Type != EventTurnFinished || terminal.Reply != "" {
		t.Fatalf("terminal event=%+v", terminal)
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil || len(history) != 4 || history[3].Role != "assistant" || history[3].Content != "" ||
		len(history[2].Artifacts) != 1 || history[2].Artifacts[0].ArtifactID != turn.Artifacts[0].ArtifactID {
		t.Fatalf("history=%+v error=%v", history, err)
	}
	// An artifact from an earlier turn must not authorize a new empty answer.
	_, err = service.SendMessage(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "Now answer without a visual"},
	)
	if !errors.Is(err, errModelResponseInvalid) || requests.Load() != 3 {
		t.Fatalf("empty next turn error=%v requests=%d", err, requests.Load())
	}
}

func newVisualOnlyPeer(t *testing.T, provider Provider, arguments []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		first := requests.Add(1) == 1
		if provider == ProviderOpenCodeGo {
			calls, finish := "", "stop"
			if first {
				calls = fmt.Sprintf(
					`[{"index":0,"id":"view-call","type":"function","function":{"name":"present_view","arguments":%q}}]`,
					arguments,
				)
				finish = "tool_calls"
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, goChatStream("private-reasoning", "", calls, finish))
			return
		}
		output := "[]"
		if first {
			output = fmt.Sprintf(
				`[{"id":"fc_view","type":"function_call","call_id":"view-call","name":"present_view","arguments":%q,"status":"completed"}]`,
				arguments,
			)
		}
		writeResponsesStream(w, fmt.Sprintf(`{"id":"response","status":"completed","output":%s}`, output))
	}))
	return peer, requests
}

func TestVisualOnlyCompletionRequiresSuccessfulPresentation(t *testing.T) {
	t.Parallel()
	var calls int
	service := newTurnService(t, &scriptedTurnModel{
		generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
			calls++
			if calls == 1 {
				return schema.AssistantMessage(
					"",
					[]schema.ToolCall{nativeTestCall("bad-view", "present_view", `{}`)},
				), nil
			}
			return schema.AssistantMessage("", nil), nil
		},
	})
	service.tools = mustBridgeTools(t, dashboardMCPServer(dashboards.NewService(nil, nil, nil)))
	conversation := mustCreateConversation(t, service)
	var events []TurnEvent
	_, err := service.SendMessageWithEvents(t.Context(), conversation.ID, MessageInput{Text: "Show temperatures"},
		func(event TurnEvent) { events = append(events, event) })
	if !errors.Is(err, errModelResponseInvalid) || calls != 2 {
		t.Fatalf("empty answer after rejected presentation: error=%v calls=%d", err, calls)
	}
	if events[len(events)-1].Type != EventTurnFailed {
		t.Fatalf("events=%+v", events)
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil || len(history) != 3 || history[2].ToolName != "present_view" || len(history[2].Artifacts) != 0 {
		t.Fatalf("history=%+v error=%v", history, err)
	}
}
