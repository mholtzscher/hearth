-- Devices retains authoritative reads for transaction-local fencing and control,
-- never canonical Adapter writes or availability overlays.
-- name: GetAdapterInstance :one
SELECT adapter_id, active_runtime_id, health_runtime_id,
       health_status, health_reason_code, health_source, health_since,
       health_evidence_at, health_source_observed_at
FROM adapter_instances WHERE adapter_id = ?;

-- name: GetEntityAvailabilityReceipt :one
SELECT fingerprint, reported_at FROM entity_availability_receipts WHERE request_id = ?;

-- name: InsertEntityAvailabilityReceipt :exec
INSERT INTO entity_availability_receipts (request_id, fingerprint, reported_at) VALUES (?, ?, ?);

-- name: GetEntityAvailabilityCurrent :one
SELECT entity_id, adapter_id, runtime_id, status, reason_code,
       source_observed_at, evidence_at, current_since,
       CAST(CASE WHEN latest_transition_receive_order IS NULL THEN 0
           WHEN typeof(latest_transition_receive_order) = 'integer' AND latest_transition_receive_order >= 1
           THEN latest_transition_receive_order ELSE -1 END AS INTEGER) AS latest_transition_receive_order,
       CAST(CASE WHEN typeof(health_revision) = 'integer' THEN health_revision ELSE -1 END AS INTEGER) AS health_revision
FROM entity_availability_current WHERE entity_id = ?;

-- name: GetEntityOwner :one
SELECT adapter_id FROM adapter_entity_mappings WHERE entity_id = ?;

-- name: UpsertEntityAvailabilityCurrent :exec
INSERT INTO entity_availability_current (
    entity_id, adapter_id, runtime_id, status, reason_code,
    source_observed_at, evidence_at, current_since,
    latest_transition_receive_order, health_revision
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(entity_id) DO UPDATE SET
    adapter_id = excluded.adapter_id, runtime_id = excluded.runtime_id,
    status = excluded.status, reason_code = excluded.reason_code,
    source_observed_at = excluded.source_observed_at, evidence_at = excluded.evidence_at,
    current_since = excluded.current_since,
    latest_transition_receive_order = excluded.latest_transition_receive_order,
    health_revision = excluded.health_revision;

-- name: ListEntityAvailabilityHistoryFirstPage :many
SELECT receive_order, status, source, reason_code, source_observed_at, observed_at
FROM health_transitions
WHERE resource_kind = 'entity' AND entity_id = sqlc.arg(entity_id)
ORDER BY receive_order DESC LIMIT sqlc.arg(page_limit);

-- name: ListEntityAvailabilityHistoryBefore :many
SELECT receive_order, status, source, reason_code, source_observed_at, observed_at
FROM health_transitions
WHERE resource_kind = 'entity' AND entity_id = sqlc.arg(entity_id)
  AND receive_order < CAST(sqlc.arg(before_receive_order) AS INTEGER)
ORDER BY receive_order DESC LIMIT sqlc.arg(page_limit);
