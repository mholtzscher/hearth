package agent //nolint:testpackage // Tests exercise the rebuild helpers and the real service.

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// historyRowFor builds one decoded persisted row for the rebuild helpers.
func historyRowFor(role schema.RoleType, content string) historyRow {
	return historyRow{role: string(role), bytes: len(content), message: &schema.Message{Role: role, Content: content}}
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
