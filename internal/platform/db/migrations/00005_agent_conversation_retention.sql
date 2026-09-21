-- +goose Up
-- Agent conversation retention moves timestamps to the fixed-width sortable UTC
-- layout (nanoseconds always printed) so TEXT ordering matches chronology, and
-- adds the indexes the whole-conversation prune scans. RFC3339Nano trims
-- trailing zeros, which made lexicographic comparison disagree with time.
-- Existing rows keep millisecond precision because SQLite's %f has no
-- sub-millisecond form.
UPDATE agent_conversations
SET created_at = strftime('%Y-%m-%dT%H:%M:%S', created_at) || '.' ||
                 substr(strftime('%f', created_at), 4) || '000000Z'
WHERE length(created_at) != 30;

UPDATE agent_messages
SET created_at = strftime('%Y-%m-%dT%H:%M:%S', created_at) || '.' ||
                 substr(strftime('%f', created_at), 4) || '000000Z'
WHERE length(created_at) != 30;

-- The prune walks conversations by creation time and checks each one's newest
-- message, so it needs both positions.
CREATE INDEX agent_conversations_created_idx
    ON agent_conversations(created_at);

CREATE INDEX agent_messages_conversation_created_idx
    ON agent_messages(conversation_id, created_at);

-- +goose Down
DROP INDEX agent_messages_conversation_created_idx;
DROP INDEX agent_conversations_created_idx;
