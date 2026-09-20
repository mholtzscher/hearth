-- name: ListMessages :many
SELECT id, role, message_json, created_at
FROM agent_messages
WHERE conversation_id = ?
ORDER BY id;

-- name: ListMessageJson :many
SELECT message_json
FROM agent_messages
WHERE conversation_id = ?
ORDER BY id;

-- name: GetFirstUserMessage :one
SELECT message_json
FROM agent_messages
WHERE conversation_id = ? AND role = ?
ORDER BY id
LIMIT 1;

-- name: InsertMessage :exec
INSERT INTO agent_messages (conversation_id, role, message_json, created_at)
VALUES (?, ?, ?, ?);

-- name: InsertMessagePair :exec
INSERT INTO agent_messages (conversation_id, role, message_json, created_at)
VALUES (?, ?, ?, ?), (?, ?, ?, ?);
