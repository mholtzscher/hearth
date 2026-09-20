-- name: CreateConversation :exec
INSERT INTO agent_conversations (id, created_at)
VALUES (?, ?);

-- name: GetConversation :one
SELECT id, created_at
FROM agent_conversations
WHERE id = ?;

-- ListConversationSummaries reads the sidebar rows newest-activity-first. The
-- newest message is joined as a plain column rather than MAX(m.created_at) so
-- sqlc types it as a nullable TEXT, matching the previous handwritten scan.
-- name: ListConversationSummaries :many
SELECT c.id, c.created_at, COUNT(m.id) AS message_count, latest.created_at AS last_message_at
FROM agent_conversations AS c
LEFT JOIN agent_messages AS m ON m.conversation_id = c.id
LEFT JOIN agent_messages AS latest ON latest.conversation_id = c.id
    AND latest.id = (
        SELECT candidate.id FROM agent_messages AS candidate
        WHERE candidate.conversation_id = c.id
        ORDER BY candidate.created_at DESC, candidate.id DESC
        LIMIT 1
    )
GROUP BY c.id
ORDER BY COALESCE(latest.created_at, c.created_at) DESC;
