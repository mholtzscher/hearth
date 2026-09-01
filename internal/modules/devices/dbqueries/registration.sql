-- name: GetActiveAdapterRuntime :one
SELECT active_runtime_id
FROM adapter_instances
WHERE adapter_id = sqlc.arg(adapter_id)
  AND archived_at IS NULL
  AND active_runtime_id = CAST(sqlc.arg(runtime_id) AS TEXT);

-- name: GetBinding :one
SELECT
    b.adapter_id,
    b.binding_key,
    b.device_id,
    b.external_device_id,
    d.kind AS device_kind,
    d.name AS device_name
FROM adapter_bindings AS b
JOIN devices AS d ON d.id = b.device_id
WHERE b.adapter_id = ? AND b.binding_key = ?;

-- name: GetBindingByExternalDeviceID :one
SELECT adapter_id, binding_key, device_id, external_device_id
FROM adapter_bindings
WHERE adapter_id = ? AND external_device_id = ?;

-- name: CreateDevice :exec
INSERT INTO devices (id, kind, name, created_at, updated_at)
VALUES (?, ?, ?, ?, ?);

-- name: UpdateDeviceDescriptor :exec
UPDATE devices
SET kind = ?, name = ?, updated_at = ?
WHERE id = ?;

-- name: CreateEntity :exec
INSERT INTO entities (
    id, device_id, name, type_id, support_json, enabled, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateEntityDescriptor :exec
UPDATE entities
SET name = ?, support_json = ?, updated_at = ?
WHERE id = ?;

-- name: CreateBinding :exec
INSERT INTO adapter_bindings (
    adapter_id, binding_key, device_id, external_device_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?);

-- name: UpdateBindingExternalID :exec
UPDATE adapter_bindings
SET external_device_id = ?, updated_at = ?
WHERE adapter_id = ? AND binding_key = ?;

-- name: GetEntityMapping :one
SELECT
    m.adapter_id,
    m.binding_key,
    m.entity_key,
    m.entity_id,
    m.external_entity_id,
    e.device_id,
    e.name AS entity_name,
    e.type_id,
    e.support_json,
    e.enabled
FROM adapter_entity_mappings AS m
JOIN entities AS e ON e.id = m.entity_id
WHERE m.adapter_id = ? AND m.binding_key = ? AND m.entity_key = ?;

-- name: GetEntityMappingByExternalID :one
SELECT adapter_id, binding_key, entity_key, entity_id, external_entity_id
FROM adapter_entity_mappings
WHERE adapter_id = ? AND external_entity_id = ?;

-- name: CreateEntityMapping :exec
INSERT INTO adapter_entity_mappings (
    adapter_id, binding_key, entity_key, entity_id, external_entity_id,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: UpdateEntityMappingExternalID :exec
UPDATE adapter_entity_mappings
SET external_entity_id = ?, updated_at = ?
WHERE adapter_id = ? AND binding_key = ? AND entity_key = ?;

-- name: GetAdapterAvailabilityBaseline :one
SELECT active_runtime_id, health_status, health_reason_code
FROM adapter_instances
WHERE adapter_id = ? AND archived_at IS NULL;

-- name: InsertEntityAvailabilityBaseline :exec
INSERT INTO health_transitions (
    resource_kind, adapter_id, entity_id, runtime_id, status, source,
    reason_code, source_observed_at, observed_at
) VALUES ('entity', ?, ?, ?, ?, ?, ?, ?, ?);
