-- name: GetProjectedAdapterHealth :one
SELECT adapter_id,
    CAST(CASE WHEN typeof(revision) = 'integer' THEN revision ELSE 0 END AS INTEGER) AS revision,
    snapshot_json, last_message_id, applied_at
FROM device_adapter_health_projection WHERE adapter_id = ?;

-- name: UpsertProjectedAdapterHealth :exec
INSERT INTO device_adapter_health_projection (adapter_id, revision, snapshot_json, last_message_id, applied_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(adapter_id) DO UPDATE SET revision = excluded.revision,
    snapshot_json = excluded.snapshot_json, last_message_id = excluded.last_message_id,
    applied_at = excluded.applied_at;

-- name: ReadAuthoritativeHealthRevision :one
SELECT revision FROM adapter_health_heads WHERE adapter_id = ?;

-- name: InvalidateOlderAvailabilityReports :exec
DELETE FROM entity_availability_current WHERE adapter_id = ? AND health_revision < ?;

-- name: ListHealthProjectionEntities :many
SELECT entity_id FROM adapter_entity_mappings WHERE adapter_id = ? ORDER BY entity_id;

-- name: GetEffectiveAvailability :one
SELECT entity_id, status, source, reason_code, current_since, evidence_at, source_observed_at,
    CAST(CASE WHEN latest_transition_receive_order IS NULL THEN 0
        WHEN typeof(latest_transition_receive_order) = 'integer' AND latest_transition_receive_order >= 1
        THEN latest_transition_receive_order ELSE -1 END AS INTEGER) AS latest_transition_receive_order
FROM entity_availability_effective WHERE entity_id = ?;

-- name: GetLatestEntityAvailabilityTransition :one
SELECT receive_order, status, source, reason_code, source_observed_at, observed_at
FROM health_transitions WHERE resource_kind = 'entity' AND entity_id = ?
ORDER BY receive_order DESC LIMIT 1;

-- name: UpsertEffectiveAvailability :exec
INSERT INTO entity_availability_effective (entity_id, status, source, reason_code,
    current_since, evidence_at, source_observed_at, latest_transition_receive_order)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(entity_id) DO UPDATE SET status = excluded.status, source = excluded.source,
    reason_code = excluded.reason_code, current_since = excluded.current_since,
    evidence_at = excluded.evidence_at, source_observed_at = excluded.source_observed_at,
    latest_transition_receive_order = excluded.latest_transition_receive_order;

-- name: InsertEffectiveAvailabilityTransition :one
INSERT INTO health_transitions (resource_kind, adapter_id, entity_id, runtime_id,
    status, source, reason_code, source_observed_at, observed_at,
    source_health_revision, source_health_update_id)
VALUES ('entity', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING receive_order;
