-- name: GetAdapterRuntime :one
SELECT runtime_id
FROM adapter_runtimes
WHERE adapter_id = sqlc.arg(adapter_id)
  AND runtime_id = CAST(sqlc.arg(runtime_id) AS TEXT);

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
SELECT *
FROM entity_read_projection
WHERE id > ?
ORDER BY id ASC
LIMIT ?;

-- name: ListEntitiesByDevice :many
SELECT *
FROM entity_read_projection
WHERE device_id = ? AND id > ?
ORDER BY id ASC
LIMIT ?;

-- name: GetEntity :one
SELECT *
FROM entity_read_projection
WHERE id = ?;

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

-- GetEntityStateSnapshot is deliberately hand-written in
-- entity_state_snapshot.go instead of sqlc-generated here: sqlc v1.31.1 cannot
-- parse SQLite table-valued functions such as json_each, and the coherent
-- snapshot read needs json_each over one JSON array parameter so a
-- cross-Automation Condition fan-out never spends one SQLite host parameter per
-- Entity.
