//go:build integration

package agent //nolint:testpackage // Tests exercise the rebuild helpers and the real service.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// TestLoadModelMessagesDropsHistoryOutsideTheBudget protects the same bound at
// the service seam: a persisted conversation whose older rows exceed the budget
// rebuilds only its newest complete turn. It fails if the whole conversation is
// loaded into the next model input.
func TestLoadModelMessagesDropsHistoryOutsideTheBudget(t *testing.T) {
	t.Parallel()
	fix := newListFixture(t)
	conversation := fix.create()
	fix.persist(conversation.ID, schema.UserMessage("older question"))
	fix.persist(conversation.ID, schema.AssistantMessage(strings.Repeat("x", historyByteBudget), nil))
	fix.persist(conversation.ID, schema.UserMessage("newest question"))
	fix.persist(conversation.ID, schema.AssistantMessage("newest answer", nil))

	history, err := fix.service.loadModelMessages(fix.ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := contentsOf(history)
	want := []string{"newest question", "newest answer"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("history = %v, want %v", got, want)
	}
}

func escapedAutomationDefinition(t *testing.T, count int) string {
	t.Helper()
	value := `"` + strings.Repeat(`\\`, count) + `"`
	definition := `{"name":"Escaped","enabled":false,"triggers":[],"variables":{"first":` + value + `,"second":` + value + `},"steps":[{"id":"wait","kind":"delay","duration_ms":1}]}`
	if _, err := automations.DecodeDefinition([]byte(definition)); err != nil {
		t.Fatalf("invalid test definition: %v", err)
	}
	return definition
}

func TestCurrentAutomationContextOverBudgetFailsAfterPersistence(t *testing.T) {
	t.Parallel()
	modelCalls := 0
	service := newTurnService(
		t,
		&scriptedTurnModel{
			generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
				modelCalls++
				return schema.AssistantMessage("unexpected", nil), nil
			},
		},
	)
	conversation := mustCreateConversation(t, service)
	artifact := `{"kind":"hearth.automation","artifact_id":"aart_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema_version":2}`
	if err := service.persistMessage(
		t.Context(),
		conversation.ID,
		&schema.Message{Role: schema.Tool, ToolName: "present_automation", ToolCallID: "old", Content: artifact},
	); err != nil {
		t.Fatal(err)
	}
	definition := escapedAutomationDefinition(t, 30000)
	input := MessageInput{
		Text:              "change this",
		AutomationContext: &AutomationContext{Mode: "create", DefinitionJSON: definition},
	}
	prepared, err := json.Marshal(prepareModelMessage(newUserMessage(input, service.provider)))
	if err != nil || len(prepared) <= historyByteBudget {
		t.Fatalf("fixture did not exceed budget: %d %v", len(prepared), err)
	}
	var events []TurnEvent
	_, err = service.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		input,
		func(event TurnEvent) { events = append(events, event) },
	)
	if err == nil || err.Error() != "agent model input exceeds history budget" {
		t.Fatalf("expected explicit budget failure, got %v", err)
	}
	if modelCalls != 0 {
		t.Fatalf("model called %d times", modelCalls)
	}
	if len(events) < 1 || events[len(events)-1].Type != EventTurnFailed || events[len(events)-1].Error != err.Error() {
		t.Fatalf("events=%+v", events)
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil || len(history) != 2 || len(history[0].Artifacts) != 1 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	var retained AutomationContext
	if err = json.Unmarshal(
		history[1].AutomationContext,
		&retained,
	); err != nil ||
		retained.DefinitionJSON != definition {
		t.Fatalf("lost definition: %v", err)
	}
}

func TestCurrentContextSharesBudgetWithWholePriorTurns(t *testing.T) {
	t.Parallel()
	modelCalls := 0
	service := newTurnService(
		t,
		&scriptedTurnModel{
			generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
				modelCalls++
				var size int
				for _, message := range messages {
					raw, err := json.Marshal(message)
					if err != nil {
						t.Fatal(err)
					}
					size += len(raw)
					if message.Content == "previous question" {
						t.Error("retained an over-budget previous turn")
					}
				}
				if size > historyByteBudget {
					t.Errorf("model messages exceed budget: %d", size)
				}
				return schema.AssistantMessage("done", nil), nil
			},
		},
	)
	conversation := mustCreateConversation(t, service)
	for _, message := range []*schema.Message{schema.UserMessage("previous question"), schema.AssistantMessage(strings.Repeat("x", 190<<10), nil)} {
		if err := service.persistMessage(t.Context(), conversation.ID, message); err != nil {
			t.Fatal(err)
		}
	}
	input := MessageInput{
		Text:              "change",
		AutomationContext: &AutomationContext{Mode: "create", DefinitionJSON: escapedAutomationDefinition(t, 10000)},
	}
	if _, err := service.SendMessage(t.Context(), conversation.ID, input); err != nil {
		t.Fatal(err)
	}
	if modelCalls != 1 {
		t.Fatalf("model calls=%d", modelCalls)
	}
}
