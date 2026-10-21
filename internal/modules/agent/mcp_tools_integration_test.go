//go:build integration

package agent //nolint:testpackage // Tests exercise the in-memory MCP bridge internals.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

// TestTracedToolRecordsRawArgumentObject proves the persisted tool call keeps the
// model's argument object: Function.Arguments must unmarshal as a JSON object,
// not the escaped JSON string literal a second marshal would produce.
func TestTracedToolRecordsRawArgumentObject(t *testing.T) {
	t.Parallel()
	service := newConversationStore(t, MinimumConversationRetention)
	conversation := mustCreateConversation(t, service)
	echo := bridgeToolByName(t, mustBridgeTools(t, newBridgeTestServer()), "echo_text")

	assembler := newTurnAssembler(context.Background(), service, conversation.ID, nil)
	ctx := withAssembler(context.Background(), assembler)
	if _, err := echo.InvokableRun(ctx, `{"text":"hi"}`); err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := service.database.QueryRow(
		`SELECT message_json FROM agent_messages WHERE conversation_id = ? AND role = ?`,
		conversation.ID, string(schema.Assistant),
	).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var call schema.Message
	if err := json.Unmarshal([]byte(stored), &call); err != nil {
		t.Fatal(err)
	}
	if len(call.ToolCalls) != 1 {
		t.Fatalf("stored tool calls = %d, want one", len(call.ToolCalls))
	}
	arguments := call.ToolCalls[0].Function.Arguments
	var object map[string]any
	if err := json.Unmarshal([]byte(arguments), &object); err != nil {
		t.Fatalf("Function.Arguments = %q, want a JSON argument object: %v", arguments, err)
	}
	if object["text"] != "hi" {
		t.Fatalf("Function.Arguments = %q, want the model's argument object", arguments)
	}
}

func TestMCPToolsReActRecoversFromSchemaRejection(t *testing.T) {
	t.Parallel()
	var handled []bridgeCountInput
	server := mcpapi.New(mcpapi.Config{Name: "recovery-test", Version: "0.0.0"})
	mcpapi.Register(server, mcpapi.Tool[bridgeCountInput, bridgeCountOutput]{
		Name: "needs_count",
		Handler: func(_ context.Context, input bridgeCountInput) (bridgeCountOutput, error) {
			handled = append(handled, input)
			return bridgeCountOutput{Summary: input.Name}, nil
		},
	})
	store := newConversationStore(t, MinimumConversationRetention)
	script := &recoveryChatModel{t: t}
	service, err := NewService(t.Context(), Config{
		DB: store.database, Tools: mustBridgeTools(t, server), ChatModel: script,
		Retention: MinimumConversationRetention,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation := mustCreateConversation(t, service)
	var events []TurnEvent
	turn, err := service.SendMessageWithEvents(t.Context(), conversation.ID, "Try the tool.", func(event TurnEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("schema rejection aborted the turn: %v", err)
	}
	if turn.Reply != "Recovered." || !reflect.DeepEqual(turn.ToolCalls, []string{"needs_count", "needs_count"}) {
		t.Fatalf("turn = %+v, want final reply and both attempts", turn)
	}
	if len(handled) != 1 || handled[0].Name != "corrected" || handled[0].Count == nil || *handled[0].Count != 2 {
		t.Fatalf("handler inputs = %+v, want only the corrected call", handled)
	}
	wantTypes := []string{
		EventTurnStarted,
		EventToolStarted,
		EventToolFinished,
		EventToolStarted,
		EventToolFinished,
		EventTurnFinished,
	}
	var types []string
	for _, event := range events {
		types = append(types, event.Type)
	}
	if !reflect.DeepEqual(types, wantTypes) || events[5].Reply != "Recovered." {
		t.Fatalf("events = %+v, want successful recovery sequence", events)
	}
	history, err := service.loadModelMessages(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 6 || history[0].Content != "Try the tool." || history[5].Content != "Recovered." {
		t.Fatalf("history = %+v, want user, two call/result pairs, and final reply", history)
	}
	for index, arguments := range []string{`{"count":2}`, `{"name":"corrected","count":2}`} {
		call, result := history[1+index*2], history[2+index*2]
		if call.Role != schema.Assistant || len(call.ToolCalls) != 1 ||
			call.ToolCalls[0].Function.Arguments != arguments ||
			result.Role != schema.Tool ||
			result.ToolCallID != call.ToolCalls[0].ID ||
			result.Content != events[2+index*2].Result {
			t.Fatalf("persisted pair = %+v / %+v, want raw attempt and streamed observation", call, result)
		}
	}
}

func TestMCPToolsBridgeTracePersistenceFailureIsFatal(t *testing.T) {
	t.Parallel()
	service := newConversationStore(t, MinimumConversationRetention)
	conversation := mustCreateConversation(t, service)
	echo := bridgeToolByName(t, mustBridgeTools(t, newBridgeTestServer()), "echo_text")
	if err := service.database.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := withAssembler(t.Context(), newTurnAssembler(t.Context(), service, conversation.ID, nil))
	if _, err := echo.InvokableRun(ctx, `{"text":"hi"}`); err == nil {
		t.Fatal("trace persistence failure became a recoverable observation")
	}
}
