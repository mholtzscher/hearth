//go:build integration

package agent //nolint:testpackage // Tests construct the service without a chat model.

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// listFixture wires the agent service to a migrated database directly:
// ListConversations touches only SQLite, so no chat model is needed.
type listFixture struct {
	t       *testing.T
	service *Service
	ctx     context.Context
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
