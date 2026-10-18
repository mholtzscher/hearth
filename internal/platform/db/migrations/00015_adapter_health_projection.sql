-- +goose Up
CREATE TABLE adapter_health_heads (
    adapter_id TEXT PRIMARY KEY REFERENCES adapter_instances(adapter_id),
    revision INTEGER NOT NULL CHECK (typeof(revision) = 'integer' AND revision >= 1),
    snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json) AND json_type(snapshot_json) = 'object'),
    traceparent TEXT NOT NULL DEFAULT '',
    tracestate TEXT NOT NULL DEFAULT ''
);
CREATE TABLE adapter_health_outbox (
    enqueue_order INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id TEXT NOT NULL UNIQUE,
    adapter_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (typeof(revision) = 'integer' AND revision >= 1),
    snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json) AND json_type(snapshot_json) = 'object'),
    enqueued_at TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    causation_id TEXT,
    traceparent TEXT NOT NULL DEFAULT '',
    tracestate TEXT NOT NULL DEFAULT ''
);
CREATE TABLE device_adapter_health_projection (
    adapter_id TEXT PRIMARY KEY,
    revision INTEGER NOT NULL CHECK (typeof(revision) = 'integer' AND revision >= 1),
    snapshot_json TEXT NOT NULL CHECK (json_valid(snapshot_json) AND json_type(snapshot_json) = 'object'),
    last_message_id TEXT NOT NULL,
    applied_at TEXT NOT NULL
);
ALTER TABLE entity_availability_current ADD COLUMN health_revision INTEGER NOT NULL DEFAULT 0 CHECK (typeof(health_revision) = 'integer' AND health_revision >= 0);
ALTER TABLE health_transitions ADD COLUMN source_health_revision INTEGER CHECK (source_health_revision IS NULL OR (typeof(source_health_revision) = 'integer' AND source_health_revision >= 1));
ALTER TABLE health_transitions ADD COLUMN source_health_update_id TEXT;
CREATE TABLE entity_availability_effective (
    entity_id TEXT PRIMARY KEY REFERENCES entities(id),
    status TEXT NOT NULL CHECK (status IN ('unknown','available','unavailable')),
    source TEXT NOT NULL CHECK (source IN ('core','entity_report','adapter_health')),
    reason_code TEXT,
    current_since TEXT NOT NULL,
    evidence_at TEXT NOT NULL,
    source_observed_at TEXT,
    latest_transition_receive_order INTEGER REFERENCES health_transitions(receive_order)
);
-- Only currently eligible legacy reports belong to the startup revision-one head.
UPDATE entity_availability_current SET health_revision = 1
WHERE EXISTS (
    SELECT 1 FROM adapter_instances AS ai
    WHERE ai.adapter_id = entity_availability_current.adapter_id
      AND ai.active_runtime_id = entity_availability_current.runtime_id
      AND ai.health_status = 'healthy'
);
DROP VIEW entity_read_projection;
CREATE VIEW entity_read_projection AS
SELECT e.id, e.device_id, m.adapter_id,
    CAST(COALESCE(e.name_override, e.name) AS TEXT) AS name,
    e.name AS adapter_name, e.name_override,
    e.type_id, e.support_json, e.enabled, e.created_at AS entity_created_at,
    s.observation_id, s.value_json, s.adapter_received_at, s.source_updated_at,
    s.observed_at, s.receive_order,
    effective.status AS availability_status,
    effective.source AS availability_source,
    effective.reason_code AS availability_reason_code,
    effective.current_since AS availability_since,
    effective.evidence_at AS availability_evidence_at,
    effective.source_observed_at AS availability_source_observed_at
FROM entities AS e
JOIN adapter_entity_mappings AS m ON m.entity_id = e.id
LEFT JOIN entity_states AS s ON s.entity_id = e.id
LEFT JOIN entity_availability_effective AS effective ON effective.entity_id = e.id;

-- +goose Down
-- Destructive: restore a consistent pre-feature database/broker backup for binary rollback.
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
DROP TABLE entity_availability_effective;
ALTER TABLE health_transitions DROP COLUMN source_health_update_id;
ALTER TABLE health_transitions DROP COLUMN source_health_revision;
ALTER TABLE entity_availability_current DROP COLUMN health_revision;
DROP TABLE device_adapter_health_projection;
DROP TABLE adapter_health_outbox;
DROP TABLE adapter_health_heads;
