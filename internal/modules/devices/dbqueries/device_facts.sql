-- name: InsertObservationDeviceFact :exec
INSERT INTO device_facts_outbox (
    fact_id, family, entity_id, variant, source_id, correlation_id, created_at,
    traceparent, tracestate, value_json, adapter_received_at, source_updated_at,
    observed_at
) VALUES (?, 'observation', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertEntityEventDeviceFact :exec
INSERT INTO device_facts_outbox (
    fact_id, family, entity_id, variant, source_id, correlation_id, created_at,
    traceparent, tracestate, reported_at, received_at, recorded_at
) VALUES (?, 'entity-event', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListPendingDeviceFacts :many
SELECT enqueue_order, fact_id, family, entity_id, variant, source_id,
       correlation_id, created_at, traceparent, tracestate,
       value_json, adapter_received_at, source_updated_at, observed_at,
       reported_at, received_at, recorded_at
FROM device_facts_outbox
ORDER BY enqueue_order
LIMIT ?;

-- name: DeleteDeviceFact :exec
DELETE FROM device_facts_outbox
WHERE fact_id = ?;
