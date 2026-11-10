//go:build integration

package agent //nolint:testpackage // Exercises persisted messages and streaming.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/labstack/echo/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

//nolint:gocognit // The journey checks streaming, persistence, and model context together.
func TestAutomationArtifactStreamHistoryAndSelectedContext(t *testing.T) {
	t.Parallel()
	artifact := automations.Artifact{
		ArtifactID:   "aart_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Presentation: automations.PresentCreate{DefinitionJSON: automationDraftDefinition},
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	server := mcpapi.New(mcpapi.Config{Name: "automation-presentation", Version: "1"})
	server.Raw().
		AddTool(&mcp.Tool{Name: "present_automation", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
		})
	calls := 0
	script := &scriptedTurnModel{
		generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			calls++
			if calls == 1 {
				found := false
				for _, message := range messages {
					if strings.Contains(message.Content, "9007199254740993") &&
						strings.Contains(message.Content, "Selected automation draft") {
						found = true
					}
				}
				if !found {
					t.Error("model did not receive exact selected draft")
				}
				return schema.AssistantMessage(
					"",
					[]schema.ToolCall{nativeTestCall("auto-call", "present_automation", `{"complete_turn":true}`)},
				), nil
			}
			return schema.AssistantMessage("Done", nil), nil
		},
	}
	service := newTurnService(t, script)
	service.tools = mustBridgeTools(t, server)
	conversation := mustCreateConversation(t, service)
	contextBody, err := json.Marshal(AutomationContext{Mode: "create", DefinitionJSON: automationDraftDefinition})
	if err != nil {
		t.Fatal(err)
	}
	router := echo.New()
	RegisterHTTPStream(router, service, nil)
	response := postStreamBodyForConversation(
		t,
		router,
		conversation.ID,
		`{"text":"edit","automation_context":`+string(contextBody)+`}`,
	)
	if response.Code != 200 {
		t.Fatalf("stream: %d %s", response.Code, response.Body.String())
	}
	events := parseStreamEvents(t, response.Body.String())
	if len(events) != 5 || events[2].Type != EventToolFinished || events[3].Type != EventAutomationPresented ||
		events[3].CallID != "auto-call" ||
		events[3].Artifact == nil ||
		events[3].Artifact.Automation == nil {
		t.Fatalf("events: %+v", events)
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(history) != 4 || history[3].Content != "" || len(history[2].Artifacts) != 1 ||
		history[2].ToolCallID != "auto-call" ||
		!strings.Contains(string(history[0].AutomationContext), "9007199254740993") {
		t.Fatalf("history: %+v", history)
	}
	got, err := json.Marshal(history[2].Artifacts[0])
	if err != nil || string(got) != string(raw) {
		t.Fatalf("history artifact: %s %v", got, err)
	}
}

func TestAutomationHistoryRetainsUnsupportedArtifactVersion(t *testing.T) {
	t.Parallel()
	service := newTurnService(t, &scriptedTurnModel{})
	conversation := mustCreateConversation(t, service)
	raw := `{"kind":"hearth.automation","artifact_id":"aart_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema_version":2,"future":{"number":9007199254740993}}`
	for _, message := range []*schema.Message{
		schema.UserMessage("before"),
		{Role: schema.Tool, ToolCallID: "future-call", ToolName: "present_automation", Content: raw},
		schema.AssistantMessage("after", nil),
	} {
		if err := service.persistMessage(t.Context(), conversation.ID, message); err != nil {
			t.Fatal(err)
		}
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil || len(history) != 3 || len(history[1].Artifacts) != 1 || history[1].ToolCallID != "future-call" {
		t.Fatalf("history: %+v %v", history, err)
	}
	encoded, err := json.Marshal(history[1].Artifacts[0])
	if err != nil || string(encoded) != raw {
		t.Fatalf("future artifact changed: %s %v", encoded, err)
	}
}
