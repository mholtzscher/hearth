-- name: GetEntityEvent :one
SELECT receive_order, event_id, adapter_id, runtime_id, entity_id,
       correlation_id, name, fingerprint, disposition, rejection_code,
       emitted_at, received_at, recorded_at
FROM entity_events
WHERE event_id = ?;

-- name: InsertEntityEvent :one
INSERT INTO entity_events (
    event_id, adapter_id, runtime_id, entity_id, correlation_id, name,
    fingerprint, disposition, rejection_code, emitted_at, received_at,
    recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING receive_order;

-- name: ListEntityEvents :many
-- A first page passes the largest receive order.
SELECT event_id, entity_id, name, disposition, rejection_code,
       emitted_at, received_at, recorded_at, receive_order
FROM entity_events
WHERE entity_id = sqlc.arg(entity_id)
  AND receive_order < sqlc.arg(before_receive_order)
ORDER BY receive_order DESC
LIMIT sqlc.arg(page_limit);

-- name: DeleteEntityEventsBefore :execrows
DELETE FROM entity_events
WHERE receive_order IN (
    SELECT receive_order
    FROM entity_events
    WHERE recorded_at < CAST(sqlc.arg(recorded_at) AS TEXT)
    ORDER BY recorded_at, receive_order
    LIMIT CAST(sqlc.arg(batch_size) AS INTEGER)
);
