//go:build integration

package agent //nolint:testpackage // Tests exercise the in-memory MCP bridge internals.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestModeledMCPFailureCannotProjectArtifact(t *testing.T) {
	t.Parallel()
	definition, err := dashboards.ParseDefinition(dashboardDefinitionFixture(t, "Unsuccessful", false))
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := dashboards.NewService(nil, nil).PresentView(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	server := mcpapi.New(mcpapi.Config{Name: "modeled-error", Version: "1"})
	server.Raw().
		AddTool(&mcp.Tool{Name: "present_view", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
		})
	steps := 0
	script := &scriptedTurnModel{
		generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			steps++
			if steps == 1 {
				return schema.AssistantMessage(
					"",
					[]schema.ToolCall{nativeTestCall("failed-id", "present_view", `{}`)},
				), nil
			}
			if messages[len(messages)-1].Content != string(raw) {
				t.Error("modeled result changed before model continuation")
			}
			return schema.AssistantMessage("Failed.", nil), nil
		},
	}
	service := newTurnService(t, script)
	service.tools = mustBridgeTools(t, server)
	conversation := mustCreateConversation(t, service)
	var events []TurnEvent
	turn, err := service.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "Try"},
		func(event TurnEvent) { events = append(events, event) },
	)
	if err != nil || len(turn.Artifacts) != 0 {
		t.Fatalf("turn=%+v err=%v", turn, err)
	}
	for _, event := range events {
		if event.Type == EventViewPresented {
			t.Fatalf("failure projected as view: %+v", event)
		}
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 || history[2].Content != string(raw) || len(history[2].Artifacts) != 0 {
		t.Fatalf("history=%+v", history)
	}
	public := publicHistoryJSON(t, service, conversation.ID)
	if strings.Contains(string(public), modeledToolFailureKey) {
		t.Fatal("private MCP status leaked")
	}
}

// TestTracedToolRecordsRawArgumentObject proves the persisted tool call keeps the
// model's argument object: Function.Arguments must unmarshal as a JSON object,
// not the escaped JSON string literal a second marshal would produce.
func TestTracedToolRecordsRawArgumentObject(t *testing.T) {
	t.Parallel()
	script := &scriptedTurnModel{
		generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			if messages[len(messages)-1].Role == schema.Tool {
				return schema.AssistantMessage("Echoed.", nil), nil
			}
			return schema.AssistantMessage(
				"",
				[]schema.ToolCall{nativeTestCall("provider-echo", "echo_text", `{"text":"hi"}`)},
			), nil
		},
	}
	service := newTurnService(t, script)
	service.tools = mustBridgeTools(t, newBridgeTestServer())
	conversation := mustCreateConversation(t, service)
	if _, err := service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "Echo hi."}); err != nil {
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

func TestMCPToolsADKRecoversFromSchemaRejection(t *testing.T) {
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
	turn, err := service.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "Try the tool."},
		func(event TurnEvent) {
			events = append(events, event)
		},
	)
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
