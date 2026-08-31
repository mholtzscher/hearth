-- name: ListDevices :many
SELECT id, kind, name
FROM devices
WHERE id > ?
ORDER BY id ASC
LIMIT ?;

-- name: GetDevice :one
SELECT id, kind, name
FROM devices
WHERE id = ?;

-- name: ListEntities :many
SELECT
    e.id,
    e.device_id,
    m.adapter_id,
    e.name,
    e.type_id,
    e.support_json,
    e.enabled,
    e.created_at AS entity_created_at,
    s.observation_id,
    s.value_json,
    s.adapter_received_at,
    s.source_updated_at,
    s.observed_at,
    s.receive_order,
    ai.active_runtime_id AS availability_runtime_id,
    ai.health_status AS adapter_health_status,
    ai.health_reason_code AS adapter_health_reason_code,
    ai.health_reason_detail AS adapter_health_reason_detail,
    ai.health_since AS adapter_health_since,
    ai.health_evidence_at AS adapter_health_evidence_at,
    ai.availability_epoch AS adapter_availability_epoch,
    current.status AS reported_availability_status,
    current.reason_code AS reported_availability_reason_code,
    current.reason_detail AS reported_availability_reason_detail,
    current.source_observed_at AS reported_availability_source_observed_at,
    current.evidence_at AS reported_availability_evidence_at,
    current.current_since AS reported_availability_since
FROM entities AS e
JOIN adapter_entity_mappings AS m ON m.entity_id = e.id
LEFT JOIN entity_states AS s ON s.entity_id = e.id
LEFT JOIN adapter_instances AS ai ON ai.adapter_id = m.adapter_id AND ai.archived_at IS NULL
LEFT JOIN entity_availability_current AS current
    ON current.entity_id = e.id
    AND current.adapter_id = m.adapter_id
    AND current.availability_epoch = ai.availability_epoch
WHERE e.id > ?
ORDER BY e.id ASC
LIMIT ?;

-- name: ListEntitiesByDevice :many
SELECT
    e.id,
    e.device_id,
    m.adapter_id,
    e.name,
    e.type_id,
    e.support_json,
    e.enabled,
    e.created_at AS entity_created_at,
    s.observation_id,
    s.value_json,
    s.adapter_received_at,
    s.source_updated_at,
    s.observed_at,
    s.receive_order,
    ai.active_runtime_id AS availability_runtime_id,
    ai.health_status AS adapter_health_status,
    ai.health_reason_code AS adapter_health_reason_code,
    ai.health_reason_detail AS adapter_health_reason_detail,
    ai.health_since AS adapter_health_since,
    ai.health_evidence_at AS adapter_health_evidence_at,
    ai.availability_epoch AS adapter_availability_epoch,
    current.status AS reported_availability_status,
    current.reason_code AS reported_availability_reason_code,
    current.reason_detail AS reported_availability_reason_detail,
    current.source_observed_at AS reported_availability_source_observed_at,
    current.evidence_at AS reported_availability_evidence_at,
    current.current_since AS reported_availability_since
FROM entities AS e
JOIN adapter_entity_mappings AS m ON m.entity_id = e.id
LEFT JOIN entity_states AS s ON s.entity_id = e.id
LEFT JOIN adapter_instances AS ai ON ai.adapter_id = m.adapter_id AND ai.archived_at IS NULL
LEFT JOIN entity_availability_current AS current
    ON current.entity_id = e.id
    AND current.adapter_id = m.adapter_id
    AND current.availability_epoch = ai.availability_epoch
WHERE e.device_id = ? AND e.id > ?
ORDER BY e.id ASC
LIMIT ?;

-- name: GetEntity :one
SELECT
    e.id,
    e.device_id,
    m.adapter_id,
    e.name,
    e.type_id,
    e.support_json,
    e.enabled,
    e.created_at AS entity_created_at,
    s.observation_id,
    s.value_json,
    s.adapter_received_at,
    s.source_updated_at,
    s.observed_at,
    s.receive_order,
    ai.active_runtime_id AS availability_runtime_id,
    ai.health_status AS adapter_health_status,
    ai.health_reason_code AS adapter_health_reason_code,
    ai.health_reason_detail AS adapter_health_reason_detail,
    ai.health_since AS adapter_health_since,
    ai.health_evidence_at AS adapter_health_evidence_at,
    ai.availability_epoch AS adapter_availability_epoch,
    current.status AS reported_availability_status,
    current.reason_code AS reported_availability_reason_code,
    current.reason_detail AS reported_availability_reason_detail,
    current.source_observed_at AS reported_availability_source_observed_at,
    current.evidence_at AS reported_availability_evidence_at,
    current.current_since AS reported_availability_since
FROM entities AS e
JOIN adapter_entity_mappings AS m ON m.entity_id = e.id
LEFT JOIN entity_states AS s ON s.entity_id = e.id
LEFT JOIN adapter_instances AS ai ON ai.adapter_id = m.adapter_id AND ai.archived_at IS NULL
LEFT JOIN entity_availability_current AS current
    ON current.entity_id = e.id
    AND current.adapter_id = m.adapter_id
    AND current.availability_epoch = ai.availability_epoch
WHERE e.id = ?;

-- name: UpdateEntityEnablement :execrows
UPDATE entities
SET enabled = sqlc.arg(enabled), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND enabled <> sqlc.arg(enabled);

-- name: GetEntityState :one
SELECT entity_id, observation_id, value_json, adapter_received_at,
       source_updated_at, observed_at, receive_order
FROM entity_states
WHERE entity_id = ?;

-- name: UpsertEntityState :exec
INSERT INTO entity_states (
    entity_id, observation_id, value_json, adapter_received_at,
    source_updated_at, observed_at, receive_order
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (entity_id) DO UPDATE SET
    observation_id = excluded.observation_id,
    value_json = excluded.value_json,
    adapter_received_at = excluded.adapter_received_at,
    source_updated_at = excluded.source_updated_at,
    observed_at = excluded.observed_at,
    receive_order = excluded.receive_order;
