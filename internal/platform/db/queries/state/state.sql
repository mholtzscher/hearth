-- name: GetEntityView :one
SELECT
    e.id,
    e.device_id,
    m.adapter_id,
    e.name,
    e.type_id,
    e.constraints_json,
    CAST((
        SELECT json_group_array(operation)
        FROM entity_operations
        WHERE entity_id = e.id
    ) AS TEXT) AS operations_json,
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
