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

-- Keyset pages of one Entity's retained Observations, newest first. A first
-- page passes the largest receive order; each filter keeps its own index.

-- name: ListEntityStateHistory :many
SELECT *
FROM observations
WHERE entity_id = ?
  AND receive_order < ?
ORDER BY receive_order DESC
LIMIT ?;

-- name: ListEntityStateUpdates :many
SELECT *
FROM observations
WHERE entity_id = ?
  AND disposition IN ('applied', 'unchanged')
  AND receive_order < ?
ORDER BY receive_order DESC
LIMIT ?;

-- name: ListEntityStateHistoryByDisposition :many
SELECT *
FROM observations
WHERE entity_id = ?
  AND disposition = ?
  AND receive_order < ?
ORDER BY receive_order DESC
LIMIT ?;
