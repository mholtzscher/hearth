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
    id, device_id, name, type_id, constraints_json, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: UpdateEntityDescriptor :exec
UPDATE entities
SET name = ?, constraints_json = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteEntityOperations :exec
DELETE FROM entity_operations WHERE entity_id = ?;

-- name: UpsertEntityOperation :exec
INSERT INTO entity_operations (entity_id, operation)
VALUES (?, ?)
ON CONFLICT (entity_id, operation) DO NOTHING;

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
    e.constraints_json
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
