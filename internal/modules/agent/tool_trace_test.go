package agent //nolint:testpackage // Tests drive the turn assembler directly against SQLite.

import (
	"context"
	"testing"
)

func countToolTraceRows(t *testing.T, service *Service, conversationID string) int {
	t.Helper()
	var count int
	if err := service.database.QueryRow(
		`SELECT COUNT(*) FROM agent_messages WHERE conversation_id = ?`, conversationID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestRecordToolCallWritesPairedRows(t *testing.T) {
	t.Parallel()
	service := newConversationStore(t, MinimumConversationRetention)
	conversation := mustCreateConversation(t, service)
	assembler := &turnAssembler{
		ctx: context.Background(), service: service, conversationID: conversation.ID,
	}

	if err := assembler.recordToolCall("noop", `{"text":"hi"}`, "ok", nil); err != nil {
		t.Fatal(err)
	}
	// One assistant call row plus its tool result row.
	if got := countToolTraceRows(t, service, conversation.ID); got != 2 {
		t.Fatalf("trace rows = %d, want a paired call and result", got)
	}
}

func TestRecordToolCallLeavesNoPartialPairWhenCanceled(t *testing.T) {
	t.Parallel()
	service := newConversationStore(t, MinimumConversationRetention)
	conversation := mustCreateConversation(t, service)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	assembler := &turnAssembler{
		ctx: canceled, service: service, conversationID: conversation.ID,
	}

	if err := assembler.recordToolCall("noop", `{"text":"hi"}`, "ok", nil); err == nil {
		t.Fatal("cancelled trace write returned no error")
	}
	if got := countToolTraceRows(t, service, conversation.ID); got != 0 {
		t.Fatalf("trace rows = %d, want no unpaired call row", got)
	}
}
