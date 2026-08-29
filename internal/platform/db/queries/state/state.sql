-- name: ListDevices :many
SELECT id, kind, name
FROM devices
WHERE id > ?
ORDER BY id ASC
LIMIT ?;

-- name: GetDevice :one
SELECT
    d.id,
    d.kind,
    d.name,
    b.adapter_id,
    b.binding_key,
    b.external_device_id
FROM devices AS d
LEFT JOIN adapter_bindings AS b ON b.device_id = d.id
WHERE d.id = ?;

-- name: ListEntities :many
SELECT
    e.id,
    e.device_id,
    m.adapter_id,
    m.binding_key,
    m.entity_key,
    m.external_entity_id,
    e.name,
    e.type_id,
    e.support_json,
    e.enabled,
    s.observation_id,
    s.value_json,
    s.adapter_received_at,
    s.source_updated_at,
    s.observed_at,
    s.receive_order
FROM entities AS e
JOIN adapter_entity_mappings AS m ON m.entity_id = e.id
LEFT JOIN entity_states AS s ON s.entity_id = e.id
WHERE e.id > ?
ORDER BY e.id ASC
LIMIT ?;

-- name: ListEntitiesByDevice :many
SELECT
    e.id,
    e.device_id,
    m.adapter_id,
    m.binding_key,
    m.entity_key,
    m.external_entity_id,
    e.name,
    e.type_id,
    e.support_json,
    e.enabled,
    s.observation_id,
    s.value_json,
    s.adapter_received_at,
    s.source_updated_at,
    s.observed_at,
    s.receive_order
FROM entities AS e
JOIN adapter_entity_mappings AS m ON m.entity_id = e.id
LEFT JOIN entity_states AS s ON s.entity_id = e.id
WHERE e.device_id = ? AND e.id > ?
ORDER BY e.id ASC
LIMIT ?;

-- name: GetEntity :one
SELECT
    e.id,
    e.device_id,
    m.adapter_id,
    m.binding_key,
    m.entity_key,
    m.external_entity_id,
    e.name,
    e.type_id,
    e.support_json,
    e.enabled,
    s.observation_id,
    s.value_json,
    s.adapter_received_at,
    s.source_updated_at,
    s.observed_at,
    s.receive_order
FROM entities AS e
JOIN adapter_entity_mappings AS m ON m.entity_id = e.id
LEFT JOIN entity_states AS s ON s.entity_id = e.id
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
