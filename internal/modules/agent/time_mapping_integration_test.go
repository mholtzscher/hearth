//go:build integration

package agent //nolint:testpackage // Tests drive the unexported timestamp helpers.

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestStoredTimestampsUseFixedWidthLayout(t *testing.T) {
	t.Parallel()
	service := newConversationStore(t, MinimumConversationRetention)
	conversation := mustCreateConversation(t, service)
	if err := service.persistMessage(
		context.Background(), conversation.ID, schema.UserMessage("hello"),
	); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT created_at FROM agent_conversations`,
		`SELECT created_at FROM agent_messages`,
	} {
		var stored string
		if err := service.database.QueryRow(query).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if _, err := time.Parse(agentTimestampLayout, stored); err != nil {
			t.Fatalf("stored timestamp %q is not the fixed-width layout: %v", stored, err)
		}
	}
}

func TestListConversationsOrdersFixedWidthTimestamps(t *testing.T) {
	t.Parallel()
	service := newConversationStore(t, MinimumConversationRetention)
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	insertConversationAt(t, service.database, "aconv_early", base)
	insertConversationAt(t, service.database, "aconv_late", base.Add(500*time.Millisecond))

	summaries, err := service.ListConversations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 || summaries[0].ID != "aconv_late" || summaries[1].ID != "aconv_early" {
		t.Fatalf("order = %+v, want late then early", summaries)
	}
}
