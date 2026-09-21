-- name: ListMessages :many
SELECT id, role, message_json, created_at
FROM agent_messages
WHERE conversation_id = ?
ORDER BY id;

-- name: ListRecentMessageJson :many
SELECT role, message_json
FROM agent_messages
WHERE conversation_id = ?
ORDER BY id DESC
LIMIT ?;

-- name: InsertMessage :exec
INSERT INTO agent_messages (conversation_id, role, message_json, created_at)
VALUES (?, ?, ?, ?);

-- name: InsertMessagePair :exec
INSERT INTO agent_messages (conversation_id, role, message_json, created_at)
VALUES (?, ?, ?, ?), (?, ?, ?, ?);
