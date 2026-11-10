package agent //nolint:testpackage // Tests exercise the rebuild helpers and the real service.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
)

// historyRowFor builds one decoded persisted row for the rebuild helpers.
func historyRowFor(role schema.RoleType, content string) historyRow {
	return historyRow{role: string(role), bytes: len(content), message: &schema.Message{Role: role, Content: content}}
}

func TestHistoryBudgetCountsExpandedSelectedView(t *testing.T) {
	t.Parallel()
	message := schema.UserMessage("Question")
	message.Extra = map[string]any{viewContextKey: json.RawMessage(testViewContext)}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := historyMessageFromRow(
		dbsqlc.ListRecentMessageJsonRow{Role: string(schema.User), MessageJson: string(raw)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.bytes <= len(raw)+1 || !strings.Contains(prepared.message.Content, "Selected view draft") {
		t.Fatalf("stored=%d expanded=%d", len(raw), prepared.bytes)
	}
	older := historyRowFor(schema.User, "older")
	older.bytes = historyByteBudget - len(raw) - 1
	selected := boundConversationHistory([]historyRow{prepared, older})
	if len(selected) != 1 || selected[0].Content != prepared.message.Content {
		t.Fatalf("history did not apply expanded byte cost: %+v", selected)
	}
}

func TestFitModelHistoryRespectsExactWholeTurnBoundary(t *testing.T) {
	t.Parallel()
	history := []*schema.Message{schema.UserMessage("question"), schema.AssistantMessage("answer", nil)}
	var size int
	for _, message := range history {
		raw, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		size += len(raw)
	}
	got, err := fitModelHistory(history, size)
	if err != nil || strings.Join(contentsOf(got), "|") != "question|answer" {
		t.Fatalf("exact fit: %v %v", contentsOf(got), err)
	}
	got, err = fitModelHistory(history, size-1)
	if err != nil || len(got) != 0 {
		t.Fatalf("partial prior turn: %v %v", contentsOf(got), err)
	}
}

func TestCacheUsageDoesNotConsumeHistoryBudget(t *testing.T) {
	t.Parallel()
	message := schema.AssistantMessage("answer", nil)
	message.Extra = map[string]any{responsesOutputKey: "private-continuation"}
	message.ResponseMeta = &schema.ResponseMeta{
		Usage: &schema.TokenUsage{PromptTokens: 2048, CompletionTokens: 16, TotalTokens: 2064},
	}
	baseline, err := json.Marshal(message)
	require.NoError(t, err)
	retainModelUsage(message, modelUsage{
		Provider: ProviderOpenAI, Model: "fixture",
		InputTokens: new(int64(2048)), CachedInputTokens: new(int64(1536)), CacheWriteInputTokens: new(int64(512)),
	})
	stored, err := json.Marshal(message)
	require.NoError(t, err)
	prepared, err := historyMessageFromRow(
		dbsqlc.ListRecentMessageJsonRow{Role: "assistant", MessageJson: string(stored)},
	)
	require.NoError(t, err)
	require.Equal(t, len(baseline), prepared.bytes)
	raw, err := json.Marshal(prepared.message)
	require.NoError(t, err)
	require.JSONEq(t, string(baseline), string(raw))
	question := schema.UserMessage("question")
	questionJSON, err := json.Marshal(question)
	require.NoError(t, err)
	selected, err := fitModelHistory([]*schema.Message{question, prepared.message}, len(questionJSON)+len(baseline))
	require.NoError(t, err)
	require.Equal(t, []string{"question", "answer"}, contentsOf(selected))
	// Preparation must not erase the durable evidence or its native counters.
	after, err := json.Marshal(message)
	require.NoError(t, err)
	require.JSONEq(t, string(stored), string(after))
}

// contentsOf projects rebuilt history to its message text.
func contentsOf(history []*schema.Message) []string {
	contents := make([]string, 0, len(history))
	for _, message := range history {
		contents = append(contents, message.Content)
	}
	return contents
}

// TestBoundConversationHistoryKeepsNewestCompleteTurns protects the model-input
// bound: only the newest rows inside historyByteBudget are rebuilt, and the
// result starts at a user message so a tool result never arrives without its
// assistant tool call. It fails if history grows unbounded or is cut mid-turn.
func TestBoundConversationHistoryKeepsNewestCompleteTurns(t *testing.T) {
	t.Parallel()
	t.Run("all rows inside the budget", func(t *testing.T) {
		t.Parallel()
		// Newest-first, as the query returns them.
		rows := []historyRow{
			historyRowFor(schema.Assistant, "newest answer"),
			historyRowFor(schema.User, "newest question"),
			historyRowFor(schema.Assistant, "older answer"),
			historyRowFor(schema.User, "older question"),
		}
		got := contentsOf(boundConversationHistory(rows))
		want := []string{"older question", "older answer", "newest question", "newest answer"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("history = %v, want %v oldest-first", got, want)
		}
	})

	t.Run("older rows exceed the byte budget", func(t *testing.T) {
		t.Parallel()
		rows := []historyRow{
			historyRowFor(schema.Assistant, "newest answer"),
			historyRowFor(schema.User, "newest question"),
			historyRowFor(schema.Tool, strings.Repeat("x", historyByteBudget)),
			historyRowFor(schema.User, "older question"),
		}
		got := contentsOf(boundConversationHistory(rows))
		want := []string{"newest question", "newest answer"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("history = %v, want only the newest complete turn %v", got, want)
		}
	})

	t.Run("window without a user message is dropped", func(t *testing.T) {
		t.Parallel()
		rows := []historyRow{
			historyRowFor(schema.Tool, strings.Repeat("x", historyByteBudget)),
			historyRowFor(schema.Assistant, "tool call"),
			historyRowFor(schema.User, "older question"),
		}
		if got := boundConversationHistory(rows); got != nil {
			t.Fatalf("history = %v, want nil: it must never open with a tool result", contentsOf(got))
		}
	})

	t.Run("empty conversation", func(t *testing.T) {
		t.Parallel()
		if got := boundConversationHistory(nil); got != nil {
			t.Fatalf("history = %v, want nil", contentsOf(got))
		}
	})
}
