-- +goose Up
ALTER TABLE devices ADD COLUMN name_override TEXT CHECK (name_override IS NULL OR length(name_override) BETWEEN 1 AND 128);
ALTER TABLE entities ADD COLUMN name_override TEXT CHECK (name_override IS NULL OR length(name_override) BETWEEN 1 AND 128);
DROP VIEW entity_read_projection;
CREATE VIEW entity_read_projection AS
SELECT e.id, e.device_id, m.adapter_id,
    CAST(COALESCE(e.name_override, e.name) AS TEXT) AS name,
    e.name AS adapter_name, e.name_override,
    e.type_id, e.support_json, e.enabled, e.created_at AS entity_created_at,
    s.observation_id, s.value_json, s.adapter_received_at, s.source_updated_at,
    s.observed_at, s.receive_order,
    ai.health_status AS adapter_health_status,
    ai.health_reason_code AS adapter_health_reason_code,
    ai.health_since AS adapter_health_since,
    ai.health_evidence_at AS adapter_health_evidence_at,
    ai.health_source_observed_at AS adapter_health_source_observed_at,
    current.status AS reported_availability_status,
    current.reason_code AS reported_availability_reason_code,
    current.source_observed_at AS reported_availability_source_observed_at,
    current.evidence_at AS reported_availability_evidence_at,
    current.current_since AS reported_availability_since
FROM entities AS e
JOIN adapter_entity_mappings AS m ON m.entity_id = e.id
LEFT JOIN entity_states AS s ON s.entity_id = e.id
LEFT JOIN adapter_instances AS ai ON ai.adapter_id = m.adapter_id
LEFT JOIN entity_availability_current AS current
    ON current.entity_id = e.id AND current.adapter_id = m.adapter_id;

-- +goose Down
-- Downgrade discards household overrides. Back up the database first.
DROP VIEW entity_read_projection;
CREATE VIEW entity_read_projection AS
SELECT e.id, e.device_id, m.adapter_id, e.name,
    e.type_id, e.support_json, e.enabled, e.created_at AS entity_created_at,
    s.observation_id, s.value_json, s.adapter_received_at, s.source_updated_at,
    s.observed_at, s.receive_order,
    ai.health_status AS adapter_health_status,
    ai.health_reason_code AS adapter_health_reason_code,
    ai.health_since AS adapter_health_since,
    ai.health_evidence_at AS adapter_health_evidence_at,
    ai.health_source_observed_at AS adapter_health_source_observed_at,
    current.status AS reported_availability_status,
    current.reason_code AS reported_availability_reason_code,
    current.source_observed_at AS reported_availability_source_observed_at,
    current.evidence_at AS reported_availability_evidence_at,
    current.current_since AS reported_availability_since
FROM entities AS e
JOIN adapter_entity_mappings AS m ON m.entity_id = e.id
LEFT JOIN entity_states AS s ON s.entity_id = e.id
LEFT JOIN adapter_instances AS ai ON ai.adapter_id = m.adapter_id
LEFT JOIN entity_availability_current AS current
    ON current.entity_id = e.id AND current.adapter_id = m.adapter_id;
ALTER TABLE entities DROP COLUMN name_override;
ALTER TABLE devices DROP COLUMN name_override;
