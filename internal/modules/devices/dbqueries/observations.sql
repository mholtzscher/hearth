-- name: GetObservation :one
SELECT receive_order, observation_id, adapter_id, runtime_id, entity_id,
       disposition, rejection_code, adapter_received_at, observed_at
FROM observations
WHERE observation_id = ?;

-- name: InsertObservation :one
INSERT INTO observations (
    observation_id, adapter_id, runtime_id, entity_id, disposition,
    rejection_code, state_value_json, adapter_received_at, source_updated_at,
    observed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING receive_order;

-- name: DeleteExpiredObservations :execrows
DELETE FROM observations
WHERE observed_at < CAST(sqlc.arg(observed_at) AS TEXT)
  AND NOT EXISTS (
      SELECT 1
      FROM entity_states
      WHERE entity_states.observation_id = observations.observation_id
  );

-- name: ListEntityStateHistoryFirstPage :many
SELECT observation_id, state_value_json, disposition, rejection_code,
       adapter_received_at, source_updated_at, observed_at, receive_order
FROM observations
WHERE entity_id = ?
ORDER BY receive_order DESC
LIMIT ?;

-- name: ListEntityStateHistoryAfter :many
SELECT observation_id, state_value_json, disposition, rejection_code,
       adapter_received_at, source_updated_at, observed_at, receive_order
FROM observations
WHERE entity_id = ?
  AND receive_order < ?
ORDER BY receive_order DESC
LIMIT ?;

-- name: ListEntityStateUpdatesFirstPage :many
SELECT observation_id, state_value_json, disposition, rejection_code,
       adapter_received_at, source_updated_at, observed_at, receive_order
FROM observations
WHERE entity_id = ?
  AND disposition IN ('applied', 'unchanged')
ORDER BY receive_order DESC
LIMIT ?;

-- name: ListEntityStateUpdatesAfter :many
SELECT observation_id, state_value_json, disposition, rejection_code,
       adapter_received_at, source_updated_at, observed_at, receive_order
FROM observations
WHERE entity_id = ?
  AND disposition IN ('applied', 'unchanged')
  AND receive_order < ?
ORDER BY receive_order DESC
LIMIT ?;

-- name: ListEntityStateHistoryByDispositionFirstPage :many
SELECT observation_id, state_value_json, disposition, rejection_code,
       adapter_received_at, source_updated_at, observed_at, receive_order
FROM observations
WHERE entity_id = ?
  AND disposition = ?
ORDER BY receive_order DESC
LIMIT ?;

-- name: ListEntityStateHistoryByDispositionAfter :many
SELECT observation_id, state_value_json, disposition, rejection_code,
       adapter_received_at, source_updated_at, observed_at, receive_order
FROM observations
WHERE entity_id = ?
  AND disposition = ?
  AND receive_order < ?
ORDER BY receive_order DESC
LIMIT ?;
