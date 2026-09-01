-- name: GetAdapterInstance :one
SELECT adapter_id, archived_at, active_runtime_id, health_runtime_id,
       health_status, health_reason_code, health_since, health_evidence_at,
       external_system_status, external_system_reason_code,
       external_system_source_observed_at, external_system_evidence_at
FROM adapter_instances
WHERE adapter_id = ?;

-- name: CreateAdapterInstance :exec
INSERT INTO adapter_instances (
    adapter_id, health_status, health_reason_code, health_since, health_evidence_at
) VALUES (?, ?, ?, ?, ?);

-- name: GetRuntimeByClaimID :one
SELECT runtime_id, claim_id, adapter_id, software_name, software_version,
       claimed_at, last_heartbeat_at, lease_expires_at, ended_at, end_reason
FROM adapter_runtimes
WHERE claim_id = ?;

-- name: GetRuntime :one
SELECT runtime_id, claim_id, adapter_id, software_name, software_version,
       claimed_at, last_heartbeat_at, lease_expires_at, ended_at, end_reason
FROM adapter_runtimes
WHERE runtime_id = ?;

-- name: GetLatestRuntimeForAdapter :one
SELECT runtime_id, claim_id, adapter_id, software_name, software_version,
       claimed_at, last_heartbeat_at, lease_expires_at, ended_at, end_reason
FROM adapter_runtimes
WHERE adapter_id = ?
ORDER BY rowid DESC
LIMIT 1;

-- name: InsertRuntime :exec
INSERT INTO adapter_runtimes (
    runtime_id, claim_id, adapter_id, software_name, software_version,
    claimed_at, lease_expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: UpdateRuntimeHeartbeat :execrows
UPDATE adapter_runtimes
SET last_heartbeat_at = ?, lease_expires_at = ?
WHERE runtime_id = ? AND adapter_id = ? AND ended_at IS NULL;

-- name: EndRuntime :execrows
UPDATE adapter_runtimes
SET ended_at = ?, end_reason = ?
WHERE runtime_id = ? AND adapter_id = ? AND ended_at IS NULL;

-- name: ListExpiredRuntimes :many
SELECT runtime_id, claim_id, adapter_id, software_name, software_version,
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
    health_since = ?,
    health_evidence_at = ?,
    external_system_status = ?,
    external_system_reason_code = ?,
    external_system_source_observed_at = ?,
    external_system_evidence_at = ?
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
    ai.archived_at,
    ai.health_status,
    ai.health_reason_code,
    ai.health_since,
    ai.health_evidence_at,
    ai.external_system_status,
    ai.external_system_reason_code,
    ai.external_system_source_observed_at,
    ai.external_system_evidence_at,
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
    ai.archived_at,
    ai.health_status,
    ai.health_reason_code,
    ai.health_since,
    ai.health_evidence_at,
    ai.external_system_status,
    ai.external_system_reason_code,
    ai.external_system_source_observed_at,
    ai.external_system_evidence_at,
    ar.runtime_id,
    ar.software_name,
    ar.software_version,
    ar.claimed_at,
    ar.last_heartbeat_at,
    ar.lease_expires_at,
    ar.ended_at
FROM adapter_instances AS ai
LEFT JOIN adapter_runtimes AS ar ON ar.runtime_id = ai.health_runtime_id
WHERE (sqlc.arg(include_archived) OR ai.archived_at IS NULL)
ORDER BY ai.adapter_id
LIMIT sqlc.arg(page_limit);

-- name: ListAdapterViewsAfter :many
SELECT
    ai.adapter_id,
    ai.archived_at,
    ai.health_status,
    ai.health_reason_code,
    ai.health_since,
    ai.health_evidence_at,
    ai.external_system_status,
    ai.external_system_reason_code,
    ai.external_system_source_observed_at,
    ai.external_system_evidence_at,
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
  AND (sqlc.arg(include_archived) OR ai.archived_at IS NULL)
ORDER BY ai.adapter_id
LIMIT sqlc.arg(page_limit);

-- name: CountAdapterBindings :one
SELECT count(*)
FROM adapter_bindings
WHERE adapter_id = ?;

-- name: ArchiveAdapter :exec
UPDATE adapter_instances
SET archived_at = ?, active_runtime_id = NULL, health_runtime_id = NULL,
    health_status = NULL, health_reason_code = NULL,
    health_since = NULL, health_evidence_at = NULL,
    external_system_status = NULL, external_system_reason_code = NULL,
    external_system_source_observed_at = NULL,
    external_system_evidence_at = NULL
WHERE adapter_id = ?;

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

-- name: GetEntityAvailabilityCurrent :one
SELECT entity_id, adapter_id, runtime_id, status, reason_code,
       source_observed_at, evidence_at, current_since,
       latest_transition_receive_order
FROM entity_availability_current
WHERE entity_id = ?;

-- name: DeleteAdapterEntityAvailability :exec
DELETE FROM entity_availability_current
WHERE adapter_id = ?;

-- name: GetEntityOwner :one
SELECT adapter_id
FROM adapter_entity_mappings
WHERE entity_id = ?;

-- name: UpsertEntityAvailabilityCurrent :exec
INSERT INTO entity_availability_current (
    entity_id, adapter_id, runtime_id, status, reason_code,
    source_observed_at, evidence_at, current_since,
    latest_transition_receive_order
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(entity_id) DO UPDATE SET
    adapter_id = excluded.adapter_id,
    runtime_id = excluded.runtime_id,
    status = excluded.status,
    reason_code = excluded.reason_code,
    source_observed_at = excluded.source_observed_at,
    evidence_at = excluded.evidence_at,
    current_since = excluded.current_since,
    latest_transition_receive_order = excluded.latest_transition_receive_order;

-- name: ListEntityAvailabilityHistoryFirstPage :many
WITH entity_lifetime AS (
    SELECT initial.adapter_id, MIN(initial.receive_order) AS starting_receive_order
    FROM health_transitions AS initial
    WHERE initial.resource_kind = 'entity' AND initial.entity_id = sqlc.arg(entity_id)
    GROUP BY initial.adapter_id
), candidates AS (
    SELECT
        transition.receive_order,
        transition.status,
        transition.source,
        transition.reason_code,
        transition.source_observed_at,
        transition.observed_at
    FROM health_transitions AS transition
    WHERE transition.resource_kind = 'entity'
      AND transition.entity_id = sqlc.arg(entity_id)

    UNION ALL

    SELECT
        transition.receive_order,
        CASE WHEN transition.status = 'unhealthy' THEN 'unavailable' ELSE 'unknown' END AS status,
        CASE WHEN transition.status = 'healthy' THEN 'core' ELSE 'adapter_health' END AS source,
        CASE
            WHEN transition.status = 'healthy' THEN 'hearth.awaiting_entity_report'
            ELSE transition.reason_code
        END AS reason_code,
        CASE WHEN transition.status = 'healthy' THEN NULL ELSE transition.source_observed_at END AS source_observed_at,
        transition.observed_at
    FROM entity_lifetime AS lifetime
    JOIN health_transitions AS transition
        ON transition.resource_kind = 'adapter'
        AND transition.adapter_id = lifetime.adapter_id
        AND transition.receive_order >= lifetime.starting_receive_order
), sequenced AS (
    SELECT
        candidates.*,
        lag(status) OVER (ORDER BY receive_order) AS prior_status,
        lag(reason_code) OVER (ORDER BY receive_order) AS prior_reason_code
    FROM candidates
), effective AS (
    SELECT receive_order, status, source, reason_code, source_observed_at, observed_at
    FROM sequenced
    WHERE prior_status IS NULL
       OR status <> prior_status
       OR reason_code IS NOT prior_reason_code
)
SELECT receive_order, status, source, reason_code, source_observed_at, observed_at
FROM effective
ORDER BY receive_order DESC
LIMIT sqlc.arg(page_limit);

-- name: ListEntityAvailabilityHistoryBefore :many
WITH entity_lifetime AS (
    SELECT initial.adapter_id, MIN(initial.receive_order) AS starting_receive_order
    FROM health_transitions AS initial
    WHERE initial.resource_kind = 'entity' AND initial.entity_id = sqlc.arg(entity_id)
    GROUP BY initial.adapter_id
), candidates AS (
    SELECT
        transition.receive_order,
        transition.status,
        transition.source,
        transition.reason_code,
        transition.source_observed_at,
        transition.observed_at
    FROM health_transitions AS transition
    WHERE transition.resource_kind = 'entity'
      AND transition.entity_id = sqlc.arg(entity_id)

    UNION ALL

    SELECT
        transition.receive_order,
        CASE WHEN transition.status = 'unhealthy' THEN 'unavailable' ELSE 'unknown' END AS status,
        CASE WHEN transition.status = 'healthy' THEN 'core' ELSE 'adapter_health' END AS source,
        CASE
            WHEN transition.status = 'healthy' THEN 'hearth.awaiting_entity_report'
            ELSE transition.reason_code
        END AS reason_code,
        CASE WHEN transition.status = 'healthy' THEN NULL ELSE transition.source_observed_at END AS source_observed_at,
        transition.observed_at
    FROM entity_lifetime AS lifetime
    JOIN health_transitions AS transition
        ON transition.resource_kind = 'adapter'
        AND transition.adapter_id = lifetime.adapter_id
        AND transition.receive_order >= lifetime.starting_receive_order
), sequenced AS (
    SELECT
        candidates.*,
        lag(status) OVER (ORDER BY receive_order) AS prior_status,
        lag(reason_code) OVER (ORDER BY receive_order) AS prior_reason_code
    FROM candidates
), effective AS (
    SELECT receive_order, status, source, reason_code, source_observed_at, observed_at
    FROM sequenced
    WHERE prior_status IS NULL
       OR status <> prior_status
       OR reason_code IS NOT prior_reason_code
)
SELECT receive_order, status, source, reason_code, source_observed_at, observed_at
FROM effective
WHERE receive_order < CAST(sqlc.arg(before_receive_order) AS INTEGER)
ORDER BY receive_order DESC
LIMIT sqlc.arg(page_limit);
