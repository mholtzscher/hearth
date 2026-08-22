-- name: GetObservationReceipt :one
SELECT receive_order, observation_id, adapter_id, entity_id, disposition,
       rejection_code, adapter_received_at, observed_at, expires_at
FROM observation_receipts
WHERE observation_id = ?;

-- name: InsertObservationReceipt :one
INSERT INTO observation_receipts (
    observation_id, adapter_id, entity_id, disposition, rejection_code,
    adapter_received_at, observed_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING receive_order;

-- name: DeleteExpiredObservationReceipts :execrows
DELETE FROM observation_receipts
WHERE julianday(expires_at) < julianday(CAST(sqlc.arg(expires_at) AS TEXT))
  AND NOT EXISTS (
      SELECT 1
      FROM entity_states
      WHERE entity_states.observation_id = observation_receipts.observation_id
  );
