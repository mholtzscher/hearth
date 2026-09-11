-- +goose Up
PRAGMA foreign_keys = ON;

CREATE TABLE devices (
    id         TEXT PRIMARY KEY CHECK (substr(id, 1, 4) = 'dev_'),
    kind       TEXT NOT NULL CHECK (length(kind) BETWEEN 1 AND 128),
    name       TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE entities (
    id           TEXT PRIMARY KEY CHECK (substr(id, 1, 4) = 'ent_'),
    device_id    TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name         TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    type_id      TEXT NOT NULL CHECK (length(type_id) BETWEEN 1 AND 128),
    support_json TEXT NOT NULL CHECK (
        json_valid(support_json) AND json_type(support_json) = 'object'
    ),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))
);

CREATE INDEX entities_device_id_idx ON entities(device_id, id);

CREATE TABLE adapter_instances (
    adapter_id                         TEXT PRIMARY KEY CHECK (
        length(adapter_id) BETWEEN 1 AND 63
        AND substr(adapter_id, 1, 1) GLOB '[a-z0-9]'
        AND adapter_id NOT GLOB '*[^a-z0-9_-]*'
    ),
    active_runtime_id                 TEXT REFERENCES adapter_runtimes(runtime_id),
    health_runtime_id                 TEXT REFERENCES adapter_runtimes(runtime_id),
    health_status                     TEXT NOT NULL CHECK (
        health_status IN ('unknown', 'healthy', 'unhealthy')
    ),
    health_reason_code                TEXT CHECK (
        health_reason_code IS NULL OR (
            length(health_reason_code) BETWEEN 1 AND 128
            AND health_reason_code GLOB '*.*'
            AND health_reason_code NOT GLOB '*[^a-z0-9._-]*'
        )
    ),
    health_source                     TEXT NOT NULL CHECK (
        health_source IN ('core', 'adapter')
    ),
    health_since                      TEXT NOT NULL,
    health_evidence_at                TEXT NOT NULL,
    health_source_observed_at         TEXT,
    CHECK (
        (health_status = 'healthy' AND health_reason_code IS NULL)
        OR (health_status IN ('unknown', 'unhealthy') AND health_reason_code IS NOT NULL)
    ),
    CHECK (
        (health_source = 'core' AND health_source_observed_at IS NULL)
        OR (health_source = 'adapter' AND health_source_observed_at IS NOT NULL)
    )
);

CREATE TABLE adapter_runtimes (
    runtime_id        TEXT PRIMARY KEY CHECK (
        length(runtime_id) = 40 AND substr(runtime_id, 1, 4) = 'run_'
    ),
    adapter_id        TEXT NOT NULL REFERENCES adapter_instances(adapter_id) ON DELETE RESTRICT,
    software_name     TEXT NOT NULL CHECK (
        length(software_name) BETWEEN 1 AND 63
        AND substr(software_name, 1, 1) GLOB '[a-z0-9]'
        AND software_name NOT GLOB '*[^a-z0-9_-]*'
    ),
    software_version  TEXT NOT NULL CHECK (length(software_version) BETWEEN 1 AND 128),
    claimed_at        TEXT NOT NULL,
    last_heartbeat_at TEXT,
    lease_expires_at  TEXT NOT NULL,
    ended_at          TEXT,
    end_reason        TEXT CHECK (
        end_reason IS NULL OR end_reason IN ('heartbeat_expired', 'stopped')
    ),
    CHECK (
        (ended_at IS NULL AND end_reason IS NULL)
        OR (ended_at IS NOT NULL AND end_reason IS NOT NULL)
    )
);

CREATE UNIQUE INDEX adapter_runtimes_one_active_idx
    ON adapter_runtimes(adapter_id)
    WHERE ended_at IS NULL;
CREATE INDEX adapter_runtimes_adapter_idx
    ON adapter_runtimes(adapter_id);
CREATE INDEX adapter_runtimes_lease_expiry_idx
    ON adapter_runtimes(lease_expires_at)
    WHERE ended_at IS NULL;

CREATE TABLE commands (
    id                     TEXT PRIMARY KEY CHECK (substr(id, 1, 4) = 'cmd_'),
    entity_id              TEXT NOT NULL REFERENCES entities(id) ON DELETE RESTRICT,
    adapter_id             TEXT NOT NULL CHECK (length(adapter_id) BETWEEN 1 AND 63),
    runtime_id             TEXT REFERENCES adapter_runtimes(runtime_id) ON DELETE RESTRICT,
    operation              TEXT NOT NULL CHECK (length(operation) BETWEEN 1 AND 63),
    parameters_json        TEXT NOT NULL CHECK (
        json_valid(parameters_json) AND json_type(parameters_json) = 'object'
    ),
    correlation_id         TEXT NOT NULL CHECK (substr(correlation_id, 1, 4) = 'cor_'),
    status                 TEXT NOT NULL CHECK (
        status IN (
            'requested', 'accepted', 'satisfied', 'dispatched', 'rejected',
            'adapter_unhealthy', 'entity_unavailable', 'outcome_timeout',
            'entity_disabled', 'internal_failure', 'interrupted'
        )
    ),
    requested_at           TEXT NOT NULL,
    deadline_at            TEXT NOT NULL,
    accepted_at            TEXT,
    completed_at           TEXT,
    outcome_observation_id TEXT UNIQUE CHECK (
        outcome_observation_id IS NULL OR substr(outcome_observation_id, 1, 4) = 'obs_'
    ),
    failure_code           TEXT CHECK (
        failure_code IS NULL OR failure_code IN (
            'adapter_unhealthy', 'entity_unavailable', 'upstream_rejected',
            'outcome_timeout', 'entity_disabled', 'internal_error', 'core_restarted'
        )
    ),
    CHECK (
        (status IN ('requested', 'accepted') AND completed_at IS NULL)
        OR (status NOT IN ('requested', 'accepted') AND completed_at IS NOT NULL)
    ),
    CHECK (
        (status = 'satisfied' AND outcome_observation_id IS NOT NULL)
        OR (status <> 'satisfied' AND outcome_observation_id IS NULL)
    ),
    CHECK (
        (status IN ('requested', 'accepted', 'satisfied', 'dispatched') AND failure_code IS NULL)
        OR (
            failure_code IS NOT NULL AND (
                (status = 'rejected' AND failure_code = 'upstream_rejected')
                OR (status = 'adapter_unhealthy' AND failure_code = 'adapter_unhealthy')
                OR (status = 'entity_unavailable' AND failure_code = 'entity_unavailable')
                OR (status = 'outcome_timeout' AND failure_code = 'outcome_timeout')
                OR (status = 'entity_disabled' AND failure_code = 'entity_disabled')
                OR (status = 'internal_failure' AND failure_code = 'internal_error')
                OR (status = 'interrupted' AND failure_code = 'core_restarted')
            )
        )
    )
);

CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC, id DESC);

CREATE TABLE adapter_bindings (
    adapter_id         TEXT NOT NULL,
    binding_key        TEXT NOT NULL,
    device_id          TEXT NOT NULL UNIQUE REFERENCES devices(id) ON DELETE CASCADE,
    external_device_id TEXT,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    PRIMARY KEY (adapter_id, binding_key),
    UNIQUE (adapter_id, external_device_id)
);

CREATE TABLE adapter_entity_mappings (
    adapter_id         TEXT NOT NULL,
    binding_key        TEXT NOT NULL,
    entity_key         TEXT NOT NULL,
    entity_id          TEXT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE CASCADE,
    external_entity_id TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    PRIMARY KEY (adapter_id, binding_key, entity_key),
    UNIQUE (adapter_id, external_entity_id),
    FOREIGN KEY (adapter_id, binding_key)
        REFERENCES adapter_bindings(adapter_id, binding_key)
        ON DELETE CASCADE
);

CREATE TABLE observations (
    receive_order       INTEGER PRIMARY KEY AUTOINCREMENT,
    observation_id      TEXT NOT NULL UNIQUE CHECK (substr(observation_id, 1, 4) = 'obs_'),
    adapter_id          TEXT NOT NULL,
    runtime_id          TEXT REFERENCES adapter_runtimes(runtime_id) ON DELETE RESTRICT,
    entity_id           TEXT NOT NULL,
    disposition         TEXT NOT NULL CHECK (
        disposition IN ('applied', 'unchanged', 'rejected')
    ),
    rejection_code      TEXT CHECK (
        rejection_code IS NULL OR rejection_code IN (
            'unknown_entity', 'wrong_adapter', 'entity_disabled',
            'invalid_value', 'stale_runtime'
        )
    ),
    state_value_json    TEXT CHECK (
        state_value_json IS NULL OR json_valid(state_value_json)
    ),
    adapter_received_at TEXT NOT NULL,
    source_updated_at   TEXT,
    -- Fixed-width UTC timestamps so the retention cutoff compares lexicographically.
    observed_at         TEXT NOT NULL,
    CHECK (
        (disposition = 'rejected'
            AND rejection_code IS NOT NULL
            AND state_value_json IS NULL)
        OR (disposition <> 'rejected'
            AND rejection_code IS NULL
            AND state_value_json IS NOT NULL)
    )
);

CREATE INDEX observations_observed_at_idx
    ON observations(observed_at);

CREATE INDEX observations_entity_history_idx
    ON observations(entity_id, receive_order DESC);

CREATE INDEX observations_entity_disposition_history_idx
    ON observations(entity_id, disposition, receive_order DESC);

CREATE INDEX observations_entity_updates_history_idx
    ON observations(entity_id, receive_order DESC)
    WHERE disposition IN ('applied', 'unchanged');

CREATE TABLE entity_states (
    entity_id           TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
    observation_id      TEXT NOT NULL UNIQUE REFERENCES observations(observation_id),
    value_json          TEXT NOT NULL CHECK (json_valid(value_json)),
    adapter_received_at TEXT NOT NULL,
    source_updated_at   TEXT,
    observed_at         TEXT NOT NULL,
    receive_order       INTEGER NOT NULL UNIQUE REFERENCES observations(receive_order)
);

CREATE TABLE entity_availability_current (
    entity_id            TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
    adapter_id           TEXT NOT NULL REFERENCES adapter_instances(adapter_id) ON DELETE RESTRICT,
    runtime_id           TEXT NOT NULL REFERENCES adapter_runtimes(runtime_id) ON DELETE RESTRICT,
    status               TEXT NOT NULL CHECK (status IN ('available', 'unavailable')),
    reason_code          TEXT CHECK (
        reason_code IS NULL OR (
            length(reason_code) BETWEEN 1 AND 128
            AND reason_code GLOB '*.*'
            AND reason_code NOT GLOB '*[^a-z0-9._-]*'
        )
    ),
    source_observed_at   TEXT NOT NULL,
    evidence_at          TEXT NOT NULL,
    current_since        TEXT NOT NULL,
    latest_transition_receive_order INTEGER,
    CHECK (
        (status = 'available' AND reason_code IS NULL)
        OR (status = 'unavailable' AND reason_code IS NOT NULL)
    )
);

CREATE TABLE entity_availability_receipts (
    request_id  TEXT PRIMARY KEY CHECK (substr(request_id, 1, 4) = 'avl_'),
    fingerprint TEXT NOT NULL CHECK (length(fingerprint) = 64),
    reported_at TEXT NOT NULL
);

CREATE TABLE health_transitions (
    receive_order      INTEGER PRIMARY KEY AUTOINCREMENT,
    resource_kind     TEXT NOT NULL CHECK (resource_kind IN ('adapter', 'entity')),
    adapter_id        TEXT NOT NULL REFERENCES adapter_instances(adapter_id) ON DELETE RESTRICT,
    entity_id         TEXT REFERENCES entities(id) ON DELETE CASCADE,
    runtime_id        TEXT REFERENCES adapter_runtimes(runtime_id) ON DELETE RESTRICT,
    status            TEXT NOT NULL CHECK (
        status IN ('unknown', 'healthy', 'unhealthy', 'available', 'unavailable')
    ),
    source            TEXT NOT NULL CHECK (
        source IN ('core', 'adapter', 'adapter_health', 'entity_report')
    ),
    reason_code       TEXT CHECK (
        reason_code IS NULL OR (
            length(reason_code) BETWEEN 1 AND 128
            AND reason_code GLOB '*.*'
            AND reason_code NOT GLOB '*[^a-z0-9._-]*'
        )
    ),
    source_observed_at TEXT,
    observed_at       TEXT NOT NULL,
    CHECK (
        (resource_kind = 'adapter' AND entity_id IS NULL
            AND status IN ('unknown', 'healthy', 'unhealthy')
            AND source IN ('core', 'adapter'))
        OR (resource_kind = 'entity' AND entity_id IS NOT NULL
            AND status IN ('unknown', 'available', 'unavailable')
            AND source IN ('core', 'adapter_health', 'entity_report'))
    ),
    CHECK (
        (status IN ('healthy', 'available') AND reason_code IS NULL)
        OR (status IN ('unknown', 'unhealthy', 'unavailable') AND reason_code IS NOT NULL)
    )
);

CREATE INDEX health_transitions_adapter_history_idx
    ON health_transitions(adapter_id, receive_order DESC)
    WHERE resource_kind = 'adapter';
CREATE INDEX health_transitions_entity_history_idx
    ON health_transitions(entity_id, receive_order DESC)
    WHERE resource_kind = 'entity';

-- Entity Events are first-seen occurrence reports, not State: the row is the
-- history record and the duplicate guard at the same time, and it holds no
-- foreign keys so a report survives runtime, ownership, and descriptor churn.
CREATE TABLE entity_events (
    receive_order  INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id       TEXT NOT NULL UNIQUE CHECK (
        length(event_id) = 40 AND substr(event_id, 1, 4) = 'evt_'
    ),
    adapter_id     TEXT NOT NULL CHECK (
        length(adapter_id) BETWEEN 1 AND 63
        AND substr(adapter_id, 1, 1) GLOB '[a-z0-9]'
        AND adapter_id NOT GLOB '*[^a-z0-9_-]*'
    ),
    runtime_id     TEXT NOT NULL CHECK (
        length(runtime_id) = 40 AND substr(runtime_id, 1, 4) = 'run_'
    ),
    entity_id      TEXT NOT NULL CHECK (
        length(entity_id) = 40 AND substr(entity_id, 1, 4) = 'ent_'
    ),
    correlation_id TEXT NOT NULL CHECK (
        length(correlation_id) = 40 AND substr(correlation_id, 1, 4) = 'cor_'
    ),
    name           TEXT NOT NULL CHECK (
        length(name) BETWEEN 1 AND 63
        AND substr(name, 1, 1) GLOB '[a-z0-9]'
        AND name NOT GLOB '*[^a-z0-9_-]*'
    ),
    -- Raw 32-byte SHA-256 of the immutable reported tuple; duplicate
    -- detection compares this hash, never mutable delivery metadata.
    fingerprint    BLOB NOT NULL CHECK (length(fingerprint) = 32),
    disposition    TEXT NOT NULL CHECK (disposition IN ('accepted', 'rejected')),
    rejection_code TEXT CHECK (
        rejection_code IS NULL OR rejection_code IN (
            'stale_runtime', 'unknown_entity', 'wrong_adapter',
            'entity_disabled', 'unsupported_event'
        )
    ),
    -- Fixed-width UTC timestamps so the retention cutoff compares lexicographically.
    emitted_at     TEXT NOT NULL,
    received_at    TEXT NOT NULL,
    recorded_at    TEXT NOT NULL,
    CHECK (
        (disposition = 'accepted' AND rejection_code IS NULL)
        OR (disposition = 'rejected' AND rejection_code IS NOT NULL)
    )
);

CREATE INDEX entity_events_entity_history_idx
    ON entity_events(entity_id, receive_order DESC);

CREATE INDEX entity_events_retention_idx
    ON entity_events(recorded_at, receive_order);

CREATE VIEW entity_read_projection AS
SELECT
    e.id,
    e.device_id,
    m.adapter_id,
    e.name,
    e.type_id,
    e.support_json,
    e.enabled,
    e.created_at AS entity_created_at,
    s.observation_id,
    s.value_json,
    s.adapter_received_at,
    s.source_updated_at,
    s.observed_at,
    s.receive_order,
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
    ON current.entity_id = e.id
    AND current.adapter_id = m.adapter_id;

-- +goose Down
DROP VIEW entity_read_projection;
DROP INDEX entity_events_retention_idx;
DROP INDEX entity_events_entity_history_idx;
DROP TABLE entity_events;
DROP INDEX health_transitions_entity_history_idx;
DROP INDEX health_transitions_adapter_history_idx;
DROP TABLE health_transitions;
DROP TABLE entity_availability_receipts;
DROP TABLE entity_availability_current;
DROP TABLE entity_states;
DROP INDEX observations_entity_updates_history_idx;
DROP INDEX observations_entity_disposition_history_idx;
DROP INDEX observations_entity_history_idx;
DROP INDEX observations_observed_at_idx;
DROP TABLE observations;
DROP TABLE adapter_entity_mappings;
DROP TABLE adapter_bindings;
DROP INDEX commands_entity_requested_idx;
DROP TABLE commands;
UPDATE adapter_instances
SET active_runtime_id = NULL, health_runtime_id = NULL;
DROP INDEX adapter_runtimes_lease_expiry_idx;
DROP INDEX adapter_runtimes_adapter_idx;
DROP INDEX adapter_runtimes_one_active_idx;
DROP TABLE adapter_runtimes;
DROP TABLE adapter_instances;
DROP INDEX entities_device_id_idx;
DROP TABLE entities;
DROP TABLE devices;
