//go:build integration

package agent //nolint:testpackage // Tests exercise the rebuild helpers and the real service.

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
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
