-- name: GetHealthHead :one
SELECT adapter_id, revision, snapshot_json, traceparent, tracestate
FROM adapter_health_heads WHERE adapter_id = ?;

-- name: UpsertHealthHead :exec
INSERT INTO adapter_health_heads (adapter_id, revision, snapshot_json, traceparent, tracestate)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(adapter_id) DO UPDATE SET revision = excluded.revision,
snapshot_json = excluded.snapshot_json, traceparent = excluded.traceparent, tracestate = excluded.tracestate;

-- name: InsertHealthUpdate :exec
INSERT INTO adapter_health_outbox (message_id, adapter_id, revision, snapshot_json,
enqueued_at, correlation_id, causation_id, traceparent, tracestate)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: OldestHealthUpdate :one
SELECT message_id, adapter_id, revision, snapshot_json, enqueued_at, correlation_id,
causation_id, traceparent, tracestate
FROM adapter_health_outbox ORDER BY enqueue_order LIMIT 1;

-- name: DeleteHealthUpdate :exec
DELETE FROM adapter_health_outbox WHERE message_id = ?;

-- name: ListAdapterInstances :many
SELECT adapter_id, active_runtime_id, health_runtime_id, health_status, health_reason_code,
health_source, health_since, health_evidence_at, health_source_observed_at
FROM adapter_instances ORDER BY adapter_id;
