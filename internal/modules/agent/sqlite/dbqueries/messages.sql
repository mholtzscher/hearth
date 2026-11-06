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

-- name: InsertMessage :execlastid
INSERT INTO agent_messages (conversation_id, role, message_json, created_at)
VALUES (?, ?, ?, ?);

-- name: GetConversationProvider :one
SELECT CAST(COALESCE(json_extract(message_json, '$.extra.hearth_model_provider'), 'openai') AS TEXT) AS provider
FROM agent_messages
WHERE conversation_id = ? AND role = 'user'
ORDER BY id LIMIT 1;
