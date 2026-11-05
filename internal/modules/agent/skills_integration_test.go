//go:build integration

package agent //nolint:testpackage // Exercises the actual ADK skill tool and retained messages.

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestRunnerLoadsAndRetainsEmbeddedSkill(t *testing.T) {
	t.Parallel()
	step := 0
	script := &scriptedTurnModel{
		generate: func(_ context.Context, messages []*schema.Message, options ...model.Option) (*schema.Message, error) {
			step++
			if step == 1 {
				assertSkillAdvertised(t, options)
				return schema.AssistantMessage(
					"",
					[]schema.ToolCall{nativeTestCall("load-native-skill", "skill", `{"skill":"hearth-automations"}`)},
				), nil
			}
			assertSkillObservation(t, messages)
			return schema.AssistantMessage("Which fan?", nil), nil
		},
	}
	service := newTurnService(t, script)
	conversation := mustCreateConversation(t, service)
	var events []TurnEvent
	if _, err := service.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "Create an automation."},
		func(event TurnEvent) { events = append(events, event) },
	); err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[1].Type != EventToolStarted || events[1].Name != "skill" ||
		events[2].Type != EventToolFinished ||
		events[3].Reply != "Which fan?" {
		t.Fatalf("skill events = %+v", events)
	}
	history, err := service.loadModelMessages(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 || history[1].ToolCalls[0].ID != "load-native-skill" ||
		history[2].ToolCallID != "load-native-skill" {
		t.Fatalf("skill history = %+v", history)
	}
	if _, err = service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "The plug fan."}); err != nil {
		t.Fatal(err)
	}
}

func assertSkillAdvertised(t *testing.T, options []model.Option) {
	t.Helper()
	for _, info := range model.GetCommonOptions(nil, options...).Tools {
		if info.Name == "skill" && strings.Contains(info.Desc, automationSkillName) {
			return
		}
	}
	t.Error("native skill catalog missing")
}

func assertSkillObservation(t *testing.T, messages []*schema.Message) {
	t.Helper()
	result := nativeToolResults(messages)["load-native-skill"]
	if !strings.Contains(result, "# Hearth Automations") || !strings.Contains(result, `"kind":"choose"`) {
		t.Errorf("skill result omitted bundled workflow/example: %q", result)
	}
}
