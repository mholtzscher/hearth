package agent //nolint:testpackage // Tests drive retention against real SQLite.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func insertConversationAt(t *testing.T, database *sql.DB, id string, createdAt time.Time) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO agent_conversations(id, created_at) VALUES (?, ?)`,
		id, encodeAgentTimestamp(createdAt),
	); err != nil {
		t.Fatal(err)
	}
}

func insertMessageAt(t *testing.T, database *sql.DB, conversationID string, createdAt time.Time) {
	t.Helper()
	raw, err := json.Marshal(schema.UserMessage("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, execErr := database.Exec(
		`INSERT INTO agent_messages(conversation_id, role, message_json, created_at) VALUES (?, ?, ?, ?)`,
		conversationID, "user", string(raw), encodeAgentTimestamp(createdAt),
	); execErr != nil {
		t.Fatal(execErr)
	}
}

func conversationIDs(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, err := database.Query(`SELECT id FROM agent_conversations ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			t.Fatal(scanErr)
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	return ids
}

func messageCount(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM agent_messages`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestPruneHistoryRetentionBoundary(t *testing.T) {
	t.Parallel()
	service := newConversationStore(t, MinimumConversationRetention)
	database := service.database
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-MinimumConversationRetention)

	// Last activity strictly before the cutoff: deleted with its messages.
	insertConversationAt(t, database, "aconv_stale", cutoff.Add(-time.Hour))
	insertMessageAt(t, database, "aconv_stale", cutoff.Add(-time.Nanosecond))
	// Last activity exactly at the cutoff: retained (strictly-older deletion).
	insertConversationAt(t, database, "aconv_boundary", cutoff.Add(-time.Hour))
	insertMessageAt(t, database, "aconv_boundary", cutoff)
	// Fresh message on an old conversation: the whole conversation survives.
	insertConversationAt(t, database, "aconv_fresh", cutoff.Add(-72*time.Hour))
	insertMessageAt(t, database, "aconv_fresh", cutoff.Add(time.Minute))
	// Untouched conversation older than the cutoff: deleted.
	insertConversationAt(t, database, "aconv_untouched", cutoff.Add(-time.Hour))

	if err := service.PruneHistory(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(conversationIDs(t, database), ","), "aconv_boundary,aconv_fresh"; got != want {
		t.Fatalf("retained conversations = %q, want %q", got, want)
	}
	if got := messageCount(t, database); got != 2 {
		t.Fatalf("retained messages = %d, want the boundary and fresh rows", got)
	}
}

func TestPruneHistoryBatchesUntilEmpty(t *testing.T) {
	t.Parallel()
	service := newConversationStore(t, MinimumConversationRetention)
	database := service.database
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-48 * time.Hour)

	// One more than a batch forces a second deletion statement.
	for index := range conversationPruneBatch + 2 {
		id := fmt.Sprintf("aconv_%032d", index)
		insertConversationAt(t, database, id, stale)
		insertMessageAt(t, database, id, stale)
	}
	insertConversationAt(t, database, "aconv_fresh", stale)
	insertMessageAt(t, database, "aconv_fresh", now.Add(-time.Minute))

	if err := service.PruneHistory(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(conversationIDs(t, database), ","), "aconv_fresh"; got != want {
		t.Fatalf("retained conversations = %q, want only %q", got, want)
	}
	if got := messageCount(t, database); got != 1 {
		t.Fatalf("retained messages = %d, want the fresh message", got)
	}
}

func TestPruneHistoryCascadesMessages(t *testing.T) {
	t.Parallel()
	service := newConversationStore(t, MinimumConversationRetention)
	database := service.database
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-48 * time.Hour)

	insertConversationAt(t, database, "aconv_stale", stale)
	insertMessageAt(t, database, "aconv_stale", stale)
	insertMessageAt(t, database, "aconv_stale", stale.Add(time.Minute))
	if got := messageCount(t, database); got != 2 {
		t.Fatalf("seeded messages = %d, want 2", got)
	}

	if err := service.PruneHistory(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(conversationIDs(t, database)) != 0 {
		t.Fatal("stale conversation survived pruning")
	}
	if got := messageCount(t, database); got != 0 {
		t.Fatalf("messages after cascade = %d, want 0", got)
	}
}

func TestPruneHistoryRejectsUnsafeInputs(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-48 * time.Hour)

	cases := []struct {
		name      string
		retention time.Duration
		now       time.Time
	}{
		{name: "zero retention", retention: 0, now: now},
		{name: "below minimum", retention: MinimumConversationRetention - time.Minute, now: now},
		{name: "zero sweep time", retention: MinimumConversationRetention, now: time.Time{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			service := newConversationStore(t, testCase.retention)
			database := service.database
			insertConversationAt(t, database, "aconv_stale", stale)
			insertMessageAt(t, database, "aconv_stale", stale)

			if err := service.PruneHistory(context.Background(), testCase.now); err == nil {
				t.Fatal("PruneHistory accepted unsafe inputs")
			}
			if got := len(conversationIDs(t, database)); got != 1 {
				t.Fatalf("conversations after rejected prune = %d, want 1 retained", got)
			}
		})
	}
}
