-- name: CreateConversation :exec
INSERT INTO agent_conversations (id, created_at)
VALUES (?, ?);

-- name: GetConversation :one
SELECT id, created_at
FROM agent_conversations
WHERE id = ?;

-- ListConversationSummaries reads the sidebar rows newest-activity-first: the
-- newest message supplies the activity time and the first user message supplies
-- the preview. Count through the conversation index separately so the latest
-- and preview lookups run once per conversation, not once per joined message.
-- Keep the nullable message columns as joins for sqlc's nullable TEXT mapping.
-- name: ListConversationSummaries :many
SELECT
    c.id,
    c.created_at,
    (SELECT COUNT(*) FROM agent_messages AS counted
     WHERE counted.conversation_id = c.id) AS message_count,
    latest.created_at AS last_message_at,
    first_user.message_json AS first_user_message_json
FROM agent_conversations AS c
LEFT JOIN agent_messages AS latest ON latest.conversation_id = c.id
    AND latest.id = (
        SELECT candidate.id FROM agent_messages AS candidate
        WHERE candidate.conversation_id = c.id
        ORDER BY candidate.created_at DESC, candidate.id DESC
        LIMIT 1
    )
LEFT JOIN agent_messages AS first_user ON first_user.id = (
    SELECT MIN(candidate.id) FROM agent_messages AS candidate
    WHERE candidate.conversation_id = c.id AND candidate.role = 'user'
)
ORDER BY COALESCE(latest.created_at, c.created_at) DESC;
