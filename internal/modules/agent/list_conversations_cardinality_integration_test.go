//go:build integration

package agent //nolint:testpackage // Reuse the real persistence-backed service fixture without a model.

import (
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

func TestConversationSummariesPreserveCountsAndPreviewAcrossMessageFanout(t *testing.T) {
	t.Parallel()
	fix := newListFixture(t)
	busy := fix.create()
	quiet := fix.create()
	empty := fix.create()
	fix.persist(busy.ID, schema.AssistantMessage("not the preview", nil))
	fix.persist(busy.ID, schema.UserMessage("first user by insertion order"))
	fix.persist(busy.ID, schema.UserMessage("later user with older timestamp"))
	for range 30 {
		fix.persist(busy.ID, schema.AssistantMessage("retained reply", nil))
	}
	fix.persist(quiet.ID, schema.AssistantMessage("assistant only", nil))
	// Activity is determined by timestamp, while preview is determined by the
	// first user ID. Neither is the arbitrary row chosen by an aggregate join.
	_, err := fix.service.database.ExecContext(
		fix.ctx,
		`UPDATE agent_conversations SET created_at='2026-01-01T00:00:00.000000000Z'`,
	)
	require.NoError(t, err)
	_, err = fix.service.database.ExecContext(fix.ctx, `UPDATE agent_messages SET created_at=CASE
WHEN conversation_id=? THEN '2026-01-03T00:00:00.000000000Z'
WHEN role='user' AND id=(SELECT MAX(id) FROM agent_messages WHERE conversation_id=? AND role='user') THEN '2026-01-01T00:00:00.000000000Z'
ELSE '2026-01-02T00:00:00.000000000Z' END`, quiet.ID, busy.ID)
	require.NoError(t, err)
	rows := fix.list()
	require.Len(t, rows, 3)
	require.Equal(t, quiet.ID, rows[0].ID)
	require.Equal(t, 1, rows[0].MessageCount)
	require.Empty(t, rows[0].Preview)
	require.Equal(t, busy.ID, rows[1].ID)
	require.Equal(t, 33, rows[1].MessageCount)
	require.Equal(t, "first user by insertion order", rows[1].Preview)
	require.NotNil(t, rows[1].LastMessageAt)
	require.True(t, rows[1].LastMessageAt.Equal(time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)))
	require.Equal(t, empty.ID, rows[2].ID)
	require.Zero(t, rows[2].MessageCount)
	require.Empty(t, rows[2].Preview)
	require.Nil(t, rows[2].LastMessageAt)
}
