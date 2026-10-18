-- name: GetAdapterInstance :one
SELECT adapter_id, active_runtime_id, health_runtime_id,
       health_status, health_reason_code, health_source, health_since,
       health_evidence_at, health_source_observed_at
FROM adapter_instances
WHERE adapter_id = ?;

-- name: CreateAdapterInstance :exec
INSERT INTO adapter_instances (
    adapter_id, health_status, health_reason_code, health_source,
    health_since, health_evidence_at
) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetRuntime :one
SELECT runtime_id, adapter_id, software_name, software_version,
       claimed_at, last_heartbeat_at, lease_expires_at, ended_at, end_reason
FROM adapter_runtimes
WHERE runtime_id = ?;

-- name: GetLatestRuntimeForAdapter :one
SELECT runtime_id, adapter_id, software_name, software_version,
       claimed_at, last_heartbeat_at, lease_expires_at, ended_at, end_reason
FROM adapter_runtimes
WHERE adapter_id = ?
ORDER BY rowid DESC
LIMIT 1;

-- name: InsertRuntime :exec
INSERT INTO adapter_runtimes (
    runtime_id, adapter_id, software_name, software_version,
    claimed_at, lease_expires_at
) VALUES (?, ?, ?, ?, ?, ?);

-- name: UpdateRuntimeHeartbeat :execrows
UPDATE adapter_runtimes
SET last_heartbeat_at = ?, lease_expires_at = ?
WHERE runtime_id = ? AND adapter_id = ? AND ended_at IS NULL;

-- name: EndRuntime :execrows
UPDATE adapter_runtimes
SET ended_at = ?, end_reason = ?
WHERE runtime_id = ? AND adapter_id = ? AND ended_at IS NULL;

-- name: ListExpiredRuntimes :many
SELECT runtime_id, adapter_id, software_name, software_version,
       claimed_at, last_heartbeat_at, lease_expires_at, ended_at, end_reason
FROM adapter_runtimes
WHERE ended_at IS NULL
  AND julianday(lease_expires_at) <= julianday(CAST(sqlc.arg(expires_at) AS TEXT))
ORDER BY lease_expires_at, runtime_id;

-- name: UpdateAdapterCurrentHealth :exec
UPDATE adapter_instances
SET active_runtime_id = ?,
    health_runtime_id = ?,
    health_status = ?,
    health_reason_code = ?,
    health_source = ?,
    health_since = ?,
    health_evidence_at = ?,
    health_source_observed_at = ?
WHERE adapter_id = ?;

-- name: InsertHealthTransition :one
INSERT INTO health_transitions (
    resource_kind, adapter_id, entity_id, runtime_id, status, source,
    reason_code, source_observed_at, observed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING receive_order;

-- name: GetAdapterView :one
SELECT
    ai.adapter_id,
    ai.health_status,
    ai.health_reason_code,
    ai.health_source,
    ai.health_since,
    ai.health_evidence_at,
    ai.health_source_observed_at,
    ar.runtime_id,
    ar.software_name,
    ar.software_version,
    ar.claimed_at,
    ar.last_heartbeat_at,
    ar.lease_expires_at,
    ar.ended_at
FROM adapter_instances AS ai
LEFT JOIN adapter_runtimes AS ar ON ar.runtime_id = ai.health_runtime_id
WHERE ai.adapter_id = ?;

-- name: ListAdapterViewsFirstPage :many
SELECT
    ai.adapter_id,
    ai.health_status,
    ai.health_reason_code,
    ai.health_source,
    ai.health_since,
    ai.health_evidence_at,
    ai.health_source_observed_at,
    ar.runtime_id,
    ar.software_name,
    ar.software_version,
    ar.claimed_at,
    ar.last_heartbeat_at,
    ar.lease_expires_at,
    ar.ended_at
FROM adapter_instances AS ai
LEFT JOIN adapter_runtimes AS ar ON ar.runtime_id = ai.health_runtime_id
ORDER BY ai.adapter_id
LIMIT sqlc.arg(page_limit);

-- name: ListAdapterViewsAfter :many
SELECT
    ai.adapter_id,
    ai.health_status,
    ai.health_reason_code,
    ai.health_source,
    ai.health_since,
    ai.health_evidence_at,
    ai.health_source_observed_at,
    ar.runtime_id,
    ar.software_name,
    ar.software_version,
    ar.claimed_at,
    ar.last_heartbeat_at,
    ar.lease_expires_at,
    ar.ended_at
FROM adapter_instances AS ai
LEFT JOIN adapter_runtimes AS ar ON ar.runtime_id = ai.health_runtime_id
WHERE ai.adapter_id > sqlc.arg(after_adapter_id)
ORDER BY ai.adapter_id
LIMIT sqlc.arg(page_limit);

-- name: ListAdapterHealthHistoryFirstPage :many
SELECT receive_order, status, source, reason_code, source_observed_at, observed_at
FROM health_transitions
WHERE resource_kind = 'adapter' AND adapter_id = ?
ORDER BY receive_order DESC
LIMIT ?;

-- name: ListAdapterHealthHistoryBefore :many
SELECT receive_order, status, source, reason_code, source_observed_at, observed_at
FROM health_transitions
WHERE resource_kind = 'adapter' AND adapter_id = ? AND receive_order < ?
ORDER BY receive_order DESC
LIMIT ?;
