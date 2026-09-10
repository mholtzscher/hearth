-- name: GetDeviceEvent :one
SELECT receive_order, event_id, adapter_id, runtime_id, entity_id,
       correlation_id, name, fingerprint, disposition, rejection_code,
       emitted_at, received_at, recorded_at
FROM device_events
WHERE event_id = ?;

-- name: InsertDeviceEvent :one
INSERT INTO device_events (
    event_id, adapter_id, runtime_id, entity_id, correlation_id, name,
    fingerprint, disposition, rejection_code, emitted_at, received_at,
    recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING receive_order;

-- name: ListEntityDeviceEventsFirstPage :many
SELECT event_id, entity_id, name, disposition, rejection_code,
       emitted_at, received_at, recorded_at, receive_order
FROM device_events
WHERE entity_id = ?
ORDER BY receive_order DESC
LIMIT ?;

-- name: ListEntityDeviceEventsAfter :many
SELECT event_id, entity_id, name, disposition, rejection_code,
       emitted_at, received_at, recorded_at, receive_order
FROM device_events
WHERE entity_id = ?
  AND receive_order < ?
ORDER BY receive_order DESC
LIMIT ?;

-- name: DeleteDeviceEventsBefore :execrows
DELETE FROM device_events
WHERE receive_order IN (
    SELECT receive_order
    FROM device_events
    WHERE recorded_at < CAST(sqlc.arg(recorded_at) AS TEXT)
    ORDER BY recorded_at, receive_order
    LIMIT CAST(sqlc.arg(batch_size) AS INTEGER)
);
