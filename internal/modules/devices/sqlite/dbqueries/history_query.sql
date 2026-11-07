-- name: HistoryEntityType :one
SELECT type_id FROM entities WHERE id = sqlc.arg(id);

-- name: HistoryHighWater :one
SELECT CAST(COALESCE(MAX(receive_order), 0) AS INTEGER) FROM observations WHERE entity_id = sqlc.arg(entity_id);

-- name: HistorySeed :one
SELECT receive_order, observation_id, disposition, adapter_received_at, source_updated_at, observed_at,
       CAST(length(CAST(state_value_json AS BLOB)) AS INTEGER) AS value_bytes, state_value_json AS state_value
FROM observations
WHERE entity_id = sqlc.arg(entity_id) AND disposition IN ('applied', 'unchanged')
  AND observed_at < CAST(sqlc.arg(before_time) AS TEXT) AND receive_order <= sqlc.arg(high_water)
ORDER BY observed_at DESC, receive_order DESC LIMIT 1;

-- Each traversal's position starts at the range end it leaves from, and the
-- reader keeps cursors inside the range, so the position bound replaces that
-- end. SQLite seeks the index by only one of two bounds on observed_at; with
-- the range bound, every batch rescanned the rows before its position.

-- name: HistoryAscendingRows :many
SELECT receive_order, observation_id, disposition, adapter_received_at, source_updated_at, observed_at,
       CAST(length(CAST(state_value_json AS BLOB)) AS INTEGER) AS value_bytes, state_value_json AS state_value
FROM observations
WHERE entity_id = sqlc.arg(entity_id) AND disposition IN ('applied', 'unchanged')
  AND observed_at >= CAST(sqlc.arg(position_time) AS TEXT) AND observed_at < CAST(sqlc.arg(to_time) AS TEXT)
  AND (observed_at, receive_order) > (CAST(sqlc.arg(position_time) AS TEXT), CAST(sqlc.arg(position_order) AS INTEGER))
  AND receive_order <= sqlc.arg(high_water)
ORDER BY observed_at ASC, receive_order ASC LIMIT sqlc.arg(batch_limit);

-- name: HistoryDescendingRows :many
SELECT receive_order, observation_id, disposition, adapter_received_at, source_updated_at, observed_at,
       CAST(length(CAST(state_value_json AS BLOB)) AS INTEGER) AS value_bytes, state_value_json AS state_value
FROM observations
WHERE entity_id = sqlc.arg(entity_id) AND disposition IN ('applied', 'unchanged')
  AND observed_at >= CAST(sqlc.arg(from_time) AS TEXT) AND observed_at <= CAST(sqlc.arg(position_time) AS TEXT)
  AND (observed_at, receive_order) < (CAST(sqlc.arg(position_time) AS TEXT), CAST(sqlc.arg(position_order) AS INTEGER))
  AND receive_order <= sqlc.arg(high_water)
ORDER BY observed_at DESC, receive_order DESC LIMIT sqlc.arg(batch_limit);
