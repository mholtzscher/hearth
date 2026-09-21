-- name: DeleteConversationsBefore :execrows
DELETE FROM agent_conversations
WHERE id IN (
    SELECT c.id
    FROM agent_conversations AS c
    WHERE c.created_at < CAST(sqlc.arg(cutoff) AS TEXT)
      AND NOT EXISTS (
          SELECT 1 FROM agent_messages AS m
          WHERE m.conversation_id = c.id AND m.created_at >= CAST(sqlc.arg(cutoff) AS TEXT)
      )
    LIMIT CAST(sqlc.arg(batch_size) AS INTEGER)
);
