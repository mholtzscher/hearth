//go:build integration

package agent //nolint:testpackage // Verifies per-turn draft authority against retained conversation history.

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

func TestComposeViewCannotEditAnEarlierTurnsDraft(t *testing.T) {
	t.Parallel()
	// The model tries exactly the same edit with and without current selection.
	// Retained history includes a previously successful edit on a different draft.
	script := &scriptedTurnModel{
		generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			last := messages[len(messages)-1]
			if last.Role == schema.Tool {
				return schema.AssistantMessage("Done.", nil), nil
			}
			call := nativeTestCall("edit-call-"+strconv.Itoa(len(messages)), composeViewToolName,
				`{"mode":"edit","changes":[{"op":"set_title","title":"New name"}]}`)
			return schema.AssistantMessage("", []schema.ToolCall{call}), nil
		},
	}
	service := newTurnService(t, script)
	configureViewAuthoring(t, service, dashboards.NewService(nil, nil, nil))
	id := mustCreateConversation(t, service).ID

	for _, tc := range []struct {
		text string
		note string
	}{
		{"select first", "Note from first draft"},
		{"no selection", ""},
		{"select second", "Note from second draft"},
	} {
		input := MessageInput{Text: tc.text}
		if tc.note != "" {
			raw := strings.ReplaceAll(string(dashboardDefinitionFixture(t, tc.text, false)), "Hello", tc.note)
			definition, err := dashboards.ParseDefinition([]byte(raw))
			require.NoError(t, err)
			input.ViewContext = &ViewContext{Definition: definition}
		}
		turn, err := service.SendMessage(t.Context(), id, input)
		require.NoError(t, err)
		if input.ViewContext == nil {
			require.Empty(t, turn.Artifacts)
			history, historyErr := service.History(t.Context(), id)
			require.NoError(t, historyErr)
			result := history[len(history)-2]
			require.Equal(t, composeViewToolName, result.ToolName)
			require.Contains(t, result.Content, `"failure_code":"draft_required"`)
			require.Empty(t, result.Artifacts)
		} else {
			require.Len(t, turn.Artifacts, 1)
			edited, parseErr := dashboards.ParseDefinition(turn.Artifacts[0].Definition)
			require.NoError(t, parseErr)
			require.Equal(t, "New name", edited.Title)
			require.JSONEq(t, string(input.ViewContext.Definition.Spec), string(edited.Spec))
		}
	}
}
