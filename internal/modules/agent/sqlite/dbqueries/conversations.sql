-- name: CreateConversation :exec
INSERT INTO agent_conversations (id, created_at)
VALUES (?, ?);

-- name: GetConversation :one
SELECT id, created_at
FROM agent_conversations
WHERE id = ?;

-- ListConversationSummaries reads the sidebar rows newest-activity-first: the
-- newest message supplies the activity time and the first user message supplies
-- the preview, so the whole list costs one query instead of one preview query
-- per conversation. Both are joined as plain columns rather than MAX()/MIN() in
-- the select list so sqlc types them as nullable TEXT, matching the previous
-- handwritten scan.
-- name: ListConversationSummaries :many
SELECT
    c.id,
    c.created_at,
    COUNT(m.id) AS message_count,
    latest.created_at AS last_message_at,
    first_user.message_json AS first_user_message_json
FROM agent_conversations AS c
LEFT JOIN agent_messages AS m ON m.conversation_id = c.id
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
GROUP BY c.id
ORDER BY COALESCE(latest.created_at, c.created_at) DESC;
