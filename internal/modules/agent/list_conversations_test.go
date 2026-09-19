package agent //nolint:testpackage // Tests construct the service without a chat model.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// listFixture wires the agent service to a migrated database directly:
// ListConversations touches only SQLite, so no chat model is needed.
type listFixture struct {
	t       *testing.T
	service *Service
	ctx     context.Context
}

func newListFixture(t *testing.T) *listFixture {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	return &listFixture{t: t, service: &Service{database: database}, ctx: context.Background()}
}

func (fix *listFixture) create() Conversation {
	fix.t.Helper()
	conversation, err := fix.service.CreateConversation(fix.ctx)
	if err != nil {
		fix.t.Fatal(err)
	}
	return conversation
}

func (fix *listFixture) persist(id string, message *schema.Message) {
	fix.t.Helper()
	if err := fix.service.persistMessage(fix.ctx, id, message); err != nil {
		fix.t.Fatal(err)
	}
}

func (fix *listFixture) list() []ConversationSummary {
	fix.t.Helper()
	summaries, err := fix.service.ListConversations(fix.ctx)
	if err != nil {
		fix.t.Fatal(err)
	}
	return summaries
}

func TestListConversationsNewestActivityFirst(t *testing.T) {
	t.Parallel()
	fix := newListFixture(t)

	older := fix.create()
	fix.persist(older.ID, schema.UserMessage("what devices do you see?"))
	fix.persist(older.ID, schema.AssistantMessage("two devices", nil))

	quieter := fix.create()

	summaries := fix.list()
	if len(summaries) != 2 {
		t.Fatalf("ListConversations returned %d conversations, want 2", len(summaries))
	}
	// The untouched conversation was created after the first message on the
	// older one, so it sorts first until the older one gets new activity.
	if summaries[0].ID != quieter.ID {
		t.Fatalf("summaries[0] = %s, want untouched %s first", summaries[0].ID, quieter.ID)
	}
	if summaries[0].MessageCount != 0 {
		t.Fatalf("untouched message count = %d, want 0", summaries[0].MessageCount)
	}
	if summaries[0].Preview != "" {
		t.Fatalf("untouched preview = %q, want empty", summaries[0].Preview)
	}
	if summaries[0].LastMessageAt != nil {
		t.Fatalf("untouched last message = %v, want nil", summaries[0].LastMessageAt)
	}
	if summaries[1].ID != older.ID {
		t.Fatalf("summaries[1] = %s, want messaged %s second", summaries[1].ID, older.ID)
	}
	if summaries[1].MessageCount != 2 {
		t.Fatalf("messaged message count = %d, want 2", summaries[1].MessageCount)
	}
	if summaries[1].Preview != "what devices do you see?" {
		t.Fatalf("preview = %q, want first user message", summaries[1].Preview)
	}
	if summaries[1].LastMessageAt == nil {
		t.Fatal("messaged last message is nil, want newest message time")
	}

	// New activity on the older conversation moves it back to the front,
	// while the preview still comes from its first user message.
	fix.persist(older.ID, schema.UserMessage("and now?"))
	summaries = fix.list()
	if summaries[0].ID != older.ID {
		t.Fatalf("after activity summaries[0] = %s, want %s", summaries[0].ID, older.ID)
	}
	if summaries[0].Preview != "what devices do you see?" {
		t.Fatalf("preview after activity = %q, want first user message", summaries[0].Preview)
	}
}

func TestListConversationsPreviewTruncates(t *testing.T) {
	t.Parallel()
	fix := newListFixture(t)

	conversation := fix.create()
	fix.persist(conversation.ID, schema.UserMessage(strings.Repeat("word ", 30)))

	summaries := fix.list()
	if len(summaries) != 1 {
		t.Fatalf("ListConversations returned %d conversations, want 1", len(summaries))
	}
	preview := summaries[0].Preview
	if got, want := len([]rune(preview)), maxPreviewRunes+1; got != want {
		t.Fatalf("preview length = %d runes, want %d (truncated plus ellipsis)", got, want)
	}
	if !strings.HasSuffix(preview, "…") {
		t.Fatalf("preview = %q, want trailing ellipsis", preview)
	}
	if !strings.HasPrefix(preview, "word word") {
		t.Fatalf("preview = %q, want leading message text", preview)
	}
}
