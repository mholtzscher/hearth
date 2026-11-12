//go:build integration

package agent //nolint:testpackage // Exercises the runner and persisted Eino messages.

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

// The synthetic catalog passes through the real MCP bridge, including excluded writes.
func searchTestTools(t *testing.T) []tool.BaseTool {
	t.Helper()
	server := newBridgeTestServer()
	for _, name := range []string{"list_devices", "get_device", "list_entities", "get_entity", "create_automation", "replace_automation"} {
		mcpapi.Register(server, mcpapi.Tool[bridgeEchoInput, bridgeEchoOutput]{
			Name: name, Description: "Synthetic catalog tool",
			Handler: func(_ context.Context, input bridgeEchoInput) (bridgeEchoOutput, error) {
				return bridgeEchoOutput{Echo: input.Text}, nil
			},
		})
	}
	return mustBridgeTools(t, server)
}

func searchOptionNames(options []model.Option) []string {
	var names []string
	for _, info := range model.GetCommonOptions(nil, options...).Tools {
		names = append(names, info.Name)
	}
	slices.Sort(names)
	return names
}

//nolint:gocognit // One search, execution, history and eviction journey.
func TestToolSearchTraceAndRetainedSelection(t *testing.T) {
	t.Parallel()
	var toolsPerCall [][]string
	step := 0
	script := &scriptedTurnModel{
		generate: func(_ context.Context, messages []*schema.Message, options ...model.Option) (*schema.Message, error) {
			step++
			toolsPerCall = append(toolsPerCall, searchOptionNames(options))
			switch step {
			case 1:
				return schema.AssistantMessage(
					"",
					[]schema.ToolCall{nativeTestCall("search-1", "tool_search", `{"query":"select:echo_text"}`)},
				), nil
			case 2:
				if nativeToolResults(messages)["search-1"] != `{"matches":["echo_text"]}` {
					t.Errorf("search result = %v", nativeToolResults(messages))
				}
				return schema.AssistantMessage(
					"",
					[]schema.ToolCall{nativeTestCall("echo-1", "echo_text", `{"text":"synthetic"}`)},
				), nil
			case 3:
				if !strings.Contains(nativeToolResults(messages)["echo-1"], "synthetic") {
					t.Errorf("echo result = %v", nativeToolResults(messages))
				}
			}
			return schema.AssistantMessage("Done.", nil), nil
		},
	}
	service := newTurnService(t, script)
	service.tools = searchTestTools(t)
	conversation := mustCreateConversation(t, service)
	var events []TurnEvent
	if _, err := service.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "Echo"},
		func(event TurnEvent) {
			events = append(events, event)
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "Continue"}); err != nil {
		t.Fatal(err)
	}
	wantInitial := []string{"get_device", "get_entity", "list_devices", "list_entities", "skill", "tool_search"}
	wantSelected := []string{
		"echo_text",
		"get_device",
		"get_entity",
		"list_devices",
		"list_entities",
		"skill",
		"tool_search",
	}
	if len(toolsPerCall) != 4 || !reflect.DeepEqual(toolsPerCall[0], wantInitial) {
		t.Fatalf("model calls = %v", toolsPerCall)
	}
	for _, names := range toolsPerCall[1:] {
		if !reflect.DeepEqual(names, wantSelected) {
			t.Errorf("selected tools = %v, want %v", names, wantSelected)
		}
	}
	// Eino restores selected tools from retained history before the next turn's first call.
	history, err := service.loadModelMessages(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertToolSearchTrace(t, history, events)
	// Once the selection's turn falls outside the bounded history, a new run
	// cannot recover that selection from its former tool_search result.
	for range 2 {
		if err = service.persistMessage(
			t.Context(),
			conversation.ID,
			schema.UserMessage(strings.Repeat("x", historyByteBudget/2)),
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "Continue again"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(toolsPerCall[len(toolsPerCall)-1], wantInitial) {
		t.Errorf("after history eviction tools = %v, want %v", toolsPerCall[len(toolsPerCall)-1], wantInitial)
	}
}

func assertToolSearchTrace(t *testing.T, history []*schema.Message, events []TurnEvent) {
	t.Helper()
	if len(history) != 8 || len(history[1].ToolCalls) != 1 || len(history[3].ToolCalls) != 1 ||
		history[1].ToolCalls[0].ID != "search-1" || history[2].ToolName != "tool_search" ||
		history[2].ToolCallID != "search-1" || history[2].Content != `{"matches":["echo_text"]}` ||
		history[3].ToolCalls[0].ID != "echo-1" || history[4].ToolCallID != "echo-1" {
		t.Fatalf("history = %+v", history)
	}
	for _, message := range history {
		if strings.Contains(message.Content, "<available-deferred-tools>") {
			t.Fatal("search reminder was persisted")
		}
	}
	var foundStart, foundEnd bool
	for _, event := range events {
		foundStart = foundStart ||
			(event.Type == EventToolStarted && event.Name == "tool_search" && event.CallID == "search-1")
		foundEnd = foundEnd ||
			(event.Type == EventToolFinished && event.Name == "tool_search" && event.CallID == "search-1" && event.Result == `{"matches":["echo_text"]}`)
	}
	if !foundStart || !foundEnd {
		t.Fatalf("search events = %+v", events)
	}
}

func TestToolSearchNoMatchAndMissingResultDoNotSelect(t *testing.T) {
	t.Parallel()
	step := 0
	script := &scriptedTurnModel{
		generate: func(_ context.Context, messages []*schema.Message, options ...model.Option) (*schema.Message, error) {
			step++
			if names := searchOptionNames(
				options,
			); !reflect.DeepEqual(
				names,
				[]string{"get_device", "get_entity", "list_devices", "list_entities", "skill", "tool_search"},
			) {
				t.Errorf("step %d tools = %v", step, names)
			}
			if step == 1 {
				return schema.AssistantMessage(
					"",
					[]schema.ToolCall{
						nativeTestCall(
							"missing-match",
							"tool_search",
							`{"query":"select:create_automation,replace_automation,not_a_tool"}`,
						),
					},
				), nil
			}
			if step == 2 && nativeToolResults(messages)["missing-match"] != `{"matches":null}` {
				t.Errorf("no-match result = %v", nativeToolResults(messages))
			}
			if step == 3 && nativeToolResults(messages)["incomplete"] != unknownToolOutcome {
				t.Errorf("missing result was not repaired: %v", nativeToolResults(messages))
			}
			return schema.AssistantMessage("Done.", nil), nil
		},
	}
	service := newTurnService(t, script)
	service.tools = searchTestTools(t)
	conversation := mustCreateConversation(t, service)
	if _, err := service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "Search"}); err != nil {
		t.Fatal(err)
	}
	if err := service.persistMessage(t.Context(), conversation.ID, schema.AssistantMessage("", []schema.ToolCall{
		nativeTestCall("incomplete", "tool_search", `{"query":"select:echo_text"}`),
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "Continue"}); err != nil {
		t.Fatal(err)
	}
	assertNoPersistedRepair(t, service, conversation.ID)
}

func TestToolSearchWithoutDynamicTools(t *testing.T) {
	t.Parallel()
	for _, tools := range [][]tool.BaseTool{nil, {stubTool{name: "list_devices"}}} {
		static, search, err := agentToolSearch(t.Context(), tools)
		if err != nil || search != nil || len(static) != len(tools) {
			t.Fatalf("static=%v search=%v err=%v", static, search, err)
		}
	}
}
