-- +goose Up
PRAGMA foreign_keys = ON;

CREATE TABLE adapter_instances (
    adapter_id                         TEXT PRIMARY KEY CHECK (
        length(adapter_id) BETWEEN 1 AND 63
        AND substr(adapter_id, 1, 1) GLOB '[a-z0-9]'
        AND adapter_id NOT GLOB '*[^a-z0-9_-]*'
    ),
    archived_at                       TEXT,
    created_at                        TEXT NOT NULL,
    updated_at                        TEXT NOT NULL,
    active_runtime_id                 TEXT REFERENCES adapter_runtimes(runtime_id),
    health_runtime_id                 TEXT REFERENCES adapter_runtimes(runtime_id),
    health_status                     TEXT CHECK (
        health_status IS NULL OR health_status IN ('unknown', 'healthy', 'unhealthy')
    ),
    health_reason_code                TEXT CHECK (
        health_reason_code IS NULL OR (
            length(health_reason_code) BETWEEN 1 AND 128
            AND health_reason_code GLOB '*.*'
            AND health_reason_code NOT GLOB '*[^a-z0-9._-]*'
        )
    ),
    health_reason_detail              TEXT CHECK (
        health_reason_detail IS NULL OR length(health_reason_detail) BETWEEN 1 AND 512
    ),
    health_since                      TEXT,
    health_evidence_at                TEXT,
    external_system_status            TEXT CHECK (
        external_system_status IS NULL OR external_system_status IN ('unknown', 'healthy', 'unhealthy')
    ),
    external_system_reason_code       TEXT CHECK (
        external_system_reason_code IS NULL OR (
            length(external_system_reason_code) BETWEEN 1 AND 128
            AND external_system_reason_code GLOB '*.*'
            AND external_system_reason_code NOT GLOB '*[^a-z0-9._-]*'
        )
    ),
    external_system_reason_detail     TEXT CHECK (
        external_system_reason_detail IS NULL OR length(external_system_reason_detail) BETWEEN 1 AND 512
    ),
    external_system_source_observed_at TEXT,
    external_system_evidence_at       TEXT,
    availability_epoch                INTEGER NOT NULL DEFAULT 0 CHECK (availability_epoch >= 0),
    latest_transition_receive_order   INTEGER,
    CHECK (
        (archived_at IS NULL AND health_status IS NOT NULL
            AND health_since IS NOT NULL AND health_evidence_at IS NOT NULL)
        OR (archived_at IS NOT NULL
            AND active_runtime_id IS NULL AND health_runtime_id IS NULL
            AND health_status IS NULL AND health_reason_code IS NULL
            AND health_reason_detail IS NULL AND health_since IS NULL
            AND health_evidence_at IS NULL
            AND external_system_status IS NULL
            AND external_system_reason_code IS NULL
            AND external_system_reason_detail IS NULL
            AND external_system_source_observed_at IS NULL
            AND external_system_evidence_at IS NULL)
    ),
    CHECK (
        (health_status = 'healthy' AND health_reason_code IS NULL AND health_reason_detail IS NULL)
        OR (health_status IN ('unknown', 'unhealthy') AND health_reason_code IS NOT NULL)
        OR health_status IS NULL
    ),
    CHECK (health_reason_detail IS NULL OR health_reason_code IS NOT NULL),
    CHECK (
        (external_system_status IS NULL
            AND external_system_reason_code IS NULL
            AND external_system_reason_detail IS NULL
            AND external_system_source_observed_at IS NULL
            AND external_system_evidence_at IS NULL)
        OR (external_system_status IN ('unknown', 'healthy')
            AND external_system_reason_code IS NULL
            AND external_system_reason_detail IS NULL
            AND external_system_source_observed_at IS NOT NULL
            AND external_system_evidence_at IS NOT NULL)
        OR (external_system_status = 'unhealthy'
            AND external_system_reason_code IS NOT NULL
            AND external_system_source_observed_at IS NOT NULL
            AND external_system_evidence_at IS NOT NULL)
    ),
    CHECK (external_system_reason_detail IS NULL OR external_system_reason_code IS NOT NULL)
);

CREATE TABLE adapter_runtimes (
    runtime_id        TEXT PRIMARY KEY CHECK (
        length(runtime_id) = 40 AND substr(runtime_id, 1, 4) = 'run_'
    ),
    claim_id          TEXT NOT NULL UNIQUE CHECK (
        length(claim_id) = 40 AND substr(claim_id, 1, 4) = 'clm_'
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
CREATE INDEX adapter_runtimes_adapter_claimed_idx
    ON adapter_runtimes(adapter_id, claimed_at DESC, runtime_id DESC);
CREATE INDEX adapter_runtimes_lease_expiry_idx
    ON adapter_runtimes(lease_expires_at)
    WHERE ended_at IS NULL;

CREATE TABLE entity_availability_current (
    entity_id            TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
    adapter_id           TEXT NOT NULL REFERENCES adapter_instances(adapter_id) ON DELETE RESTRICT,
    runtime_id           TEXT NOT NULL REFERENCES adapter_runtimes(runtime_id) ON DELETE RESTRICT,
    availability_epoch   INTEGER NOT NULL CHECK (availability_epoch >= 0),
    status               TEXT NOT NULL CHECK (status IN ('available', 'unavailable')),
    reason_code          TEXT CHECK (
        reason_code IS NULL OR (
            length(reason_code) BETWEEN 1 AND 128
            AND reason_code GLOB '*.*'
            AND reason_code NOT GLOB '*[^a-z0-9._-]*'
        )
    ),
    reason_detail        TEXT CHECK (
        reason_detail IS NULL OR length(reason_detail) BETWEEN 1 AND 512
    ),
    source_observed_at   TEXT NOT NULL,
    evidence_at          TEXT NOT NULL,
    current_since        TEXT NOT NULL,
    latest_transition_receive_order INTEGER,
    CHECK (
        (status = 'available' AND reason_code IS NULL AND reason_detail IS NULL)
        OR (status = 'unavailable' AND reason_code IS NOT NULL)
    ),
    CHECK (reason_detail IS NULL OR reason_code IS NOT NULL)
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
        source IN ('core', 'external_system', 'adapter_health', 'entity_report')
    ),
    reason_code       TEXT CHECK (
        reason_code IS NULL OR (
            length(reason_code) BETWEEN 1 AND 128
            AND reason_code GLOB '*.*'
            AND reason_code NOT GLOB '*[^a-z0-9._-]*'
        )
    ),
    reason_detail     TEXT CHECK (
        reason_detail IS NULL OR length(reason_detail) BETWEEN 1 AND 512
    ),
    source_observed_at TEXT,
    observed_at       TEXT NOT NULL,
    CHECK (
        (resource_kind = 'adapter' AND entity_id IS NULL
            AND status IN ('unknown', 'healthy', 'unhealthy')
            AND source IN ('core', 'external_system'))
        OR (resource_kind = 'entity' AND entity_id IS NOT NULL
            AND status IN ('unknown', 'available', 'unavailable')
            AND source IN ('core', 'adapter_health', 'entity_report'))
    ),
    CHECK (
        (status IN ('healthy', 'available') AND reason_code IS NULL AND reason_detail IS NULL)
        OR (status IN ('unknown', 'unhealthy', 'unavailable') AND reason_code IS NOT NULL)
    ),
    CHECK (reason_detail IS NULL OR reason_code IS NOT NULL)
);

CREATE INDEX health_transitions_adapter_history_idx
    ON health_transitions(adapter_id, receive_order DESC)
    WHERE resource_kind = 'adapter';
CREATE INDEX health_transitions_entity_history_idx
    ON health_transitions(entity_id, receive_order DESC)
    WHERE resource_kind = 'entity';

CREATE TABLE entity_ownership_intervals (
    entity_id             TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    adapter_id            TEXT NOT NULL REFERENCES adapter_instances(adapter_id) ON DELETE RESTRICT,
    starting_receive_order INTEGER NOT NULL CHECK (starting_receive_order >= 0),
    ending_receive_order   INTEGER CHECK (
        ending_receive_order IS NULL OR ending_receive_order >= starting_receive_order
    ),
    PRIMARY KEY (entity_id, starting_receive_order)
);

CREATE UNIQUE INDEX entity_ownership_intervals_one_open_idx
    ON entity_ownership_intervals(entity_id)
    WHERE ending_receive_order IS NULL;
CREATE INDEX entity_ownership_intervals_history_idx
    ON entity_ownership_intervals(entity_id, starting_receive_order, ending_receive_order);

INSERT INTO adapter_instances (
    adapter_id, created_at, updated_at, health_status, health_reason_code,
    health_since, health_evidence_at
)
SELECT
    adapter_id,
    MIN(created_at),
    MAX(updated_at),
    'unknown',
    'hearth.awaiting_runtime',
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM adapter_bindings
GROUP BY adapter_id;

INSERT INTO entity_ownership_intervals (
    entity_id, adapter_id, starting_receive_order
)
SELECT entity_id, adapter_id, 0
FROM adapter_entity_mappings;

DROP INDEX commands_entity_requested_idx;
ALTER TABLE commands RENAME TO commands_before_adapter_health;

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
            'requested', 'accepted', 'satisfied', 'rejected',
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
        (status IN ('requested', 'accepted', 'satisfied') AND failure_code IS NULL)
        OR (status = 'rejected' AND failure_code = 'upstream_rejected')
        OR (status = 'adapter_unhealthy' AND failure_code = 'adapter_unhealthy')
        OR (status = 'entity_unavailable' AND failure_code = 'entity_unavailable')
        OR (status = 'outcome_timeout' AND failure_code = 'outcome_timeout')
        OR (status = 'entity_disabled' AND failure_code = 'entity_disabled')
        OR (status = 'internal_failure' AND failure_code = 'internal_error')
        OR (status = 'interrupted' AND failure_code = 'core_restarted')
    )
);

INSERT INTO commands (
    id, entity_id, adapter_id, runtime_id, operation, parameters_json,
    correlation_id, status, requested_at, deadline_at, accepted_at,
    completed_at, outcome_observation_id, failure_code
)
SELECT
    id, entity_id, adapter_id, NULL, operation, parameters_json,
    correlation_id,
    CASE status WHEN 'adapter_unavailable' THEN 'adapter_unhealthy' ELSE status END,
    requested_at, deadline_at, accepted_at, completed_at,
    outcome_observation_id,
    CASE failure_code WHEN 'adapter_unavailable' THEN 'adapter_unhealthy' ELSE failure_code END
FROM commands_before_adapter_health;

DROP TABLE commands_before_adapter_health;
CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC, id DESC);

CREATE TABLE entity_states_before_adapter_health AS
SELECT entity_id, observation_id, value_json, adapter_received_at,
       source_updated_at, observed_at, receive_order
FROM entity_states;
DROP TABLE entity_states;

DROP INDEX observation_receipts_expiry_idx;
ALTER TABLE observation_receipts RENAME TO observation_receipts_before_adapter_health;

CREATE TABLE observation_receipts (
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
    adapter_received_at TEXT NOT NULL,
    observed_at         TEXT NOT NULL,
    expires_at          TEXT NOT NULL,
    CHECK (
        (disposition = 'rejected' AND rejection_code IS NOT NULL)
        OR (disposition <> 'rejected' AND rejection_code IS NULL)
    )
);

INSERT INTO observation_receipts (
    receive_order, observation_id, adapter_id, runtime_id, entity_id,
    disposition, rejection_code, adapter_received_at, observed_at, expires_at
)
SELECT
    receive_order, observation_id, adapter_id, NULL, entity_id,
    disposition, rejection_code, adapter_received_at, observed_at, expires_at
FROM observation_receipts_before_adapter_health;

DROP TABLE observation_receipts_before_adapter_health;
CREATE INDEX observation_receipts_expiry_idx
    ON observation_receipts(expires_at);

CREATE TABLE entity_states (
    entity_id           TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
    observation_id      TEXT NOT NULL UNIQUE REFERENCES observation_receipts(observation_id),
    value_json          TEXT NOT NULL CHECK (json_valid(value_json)),
    adapter_received_at TEXT NOT NULL,
    source_updated_at   TEXT,
    observed_at         TEXT NOT NULL,
    receive_order       INTEGER NOT NULL UNIQUE REFERENCES observation_receipts(receive_order)
);

INSERT INTO entity_states (
    entity_id, observation_id, value_json, adapter_received_at,
    source_updated_at, observed_at, receive_order
)
SELECT
    entity_id, observation_id, value_json, adapter_received_at,
    source_updated_at, observed_at, receive_order
FROM entity_states_before_adapter_health;

DROP TABLE entity_states_before_adapter_health;

-- +goose Down
PRAGMA foreign_keys = ON;

DROP INDEX commands_entity_requested_idx;
ALTER TABLE commands RENAME TO commands_after_adapter_health;

CREATE TABLE commands (
    id                     TEXT PRIMARY KEY CHECK (substr(id, 1, 4) = 'cmd_'),
    entity_id              TEXT NOT NULL REFERENCES entities(id) ON DELETE RESTRICT,
    adapter_id             TEXT NOT NULL CHECK (length(adapter_id) BETWEEN 1 AND 63),
    operation              TEXT NOT NULL CHECK (length(operation) BETWEEN 1 AND 63),
    parameters_json        TEXT NOT NULL CHECK (
        json_valid(parameters_json) AND json_type(parameters_json) = 'object'
    ),
    correlation_id         TEXT NOT NULL CHECK (substr(correlation_id, 1, 4) = 'cor_'),
    status                 TEXT NOT NULL CHECK (
        status IN (
            'requested', 'accepted', 'satisfied', 'rejected',
            'adapter_unavailable', 'outcome_timeout', 'entity_disabled',
            'internal_failure', 'interrupted'
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
            'adapter_unavailable', 'upstream_rejected', 'outcome_timeout',
            'entity_disabled', 'internal_error', 'core_restarted'
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
        (status IN ('requested', 'accepted', 'satisfied') AND failure_code IS NULL)
        OR (status = 'rejected' AND failure_code = 'upstream_rejected')
        OR (status = 'adapter_unavailable' AND failure_code = 'adapter_unavailable')
        OR (status = 'outcome_timeout' AND failure_code = 'outcome_timeout')
        OR (status = 'entity_disabled' AND failure_code = 'entity_disabled')
        OR (status = 'internal_failure' AND failure_code = 'internal_error')
        OR (status = 'interrupted' AND failure_code = 'core_restarted')
    )
);

INSERT INTO commands (
    id, entity_id, adapter_id, operation, parameters_json, correlation_id,
    status, requested_at, deadline_at, accepted_at, completed_at,
    outcome_observation_id, failure_code
)
SELECT
    id, entity_id, adapter_id, operation, parameters_json, correlation_id,
    CASE status
        WHEN 'adapter_unhealthy' THEN 'adapter_unavailable'
        WHEN 'entity_unavailable' THEN 'rejected'
        ELSE status
    END,
    requested_at, deadline_at, accepted_at, completed_at,
    outcome_observation_id,
    CASE failure_code
        WHEN 'adapter_unhealthy' THEN 'adapter_unavailable'
        WHEN 'entity_unavailable' THEN 'upstream_rejected'
        ELSE failure_code
    END
FROM commands_after_adapter_health;

DROP TABLE commands_after_adapter_health;
CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC, id DESC);

DELETE FROM observation_receipts
WHERE rejection_code = 'stale_runtime';

CREATE TABLE entity_states_after_adapter_health AS
SELECT entity_id, observation_id, value_json, adapter_received_at,
       source_updated_at, observed_at, receive_order
FROM entity_states;
DROP TABLE entity_states;

DROP INDEX observation_receipts_expiry_idx;
ALTER TABLE observation_receipts RENAME TO observation_receipts_after_adapter_health;

CREATE TABLE observation_receipts (
    receive_order       INTEGER PRIMARY KEY AUTOINCREMENT,
    observation_id      TEXT NOT NULL UNIQUE CHECK (substr(observation_id, 1, 4) = 'obs_'),
    adapter_id          TEXT NOT NULL,
    entity_id           TEXT NOT NULL,
    disposition         TEXT NOT NULL CHECK (
        disposition IN ('applied', 'unchanged', 'rejected')
    ),
    rejection_code      TEXT CHECK (
        rejection_code IS NULL OR rejection_code IN (
            'unknown_entity', 'wrong_adapter', 'entity_disabled', 'invalid_value'
        )
    ),
    adapter_received_at TEXT NOT NULL,
    observed_at         TEXT NOT NULL,
    expires_at          TEXT NOT NULL,
    CHECK (
        (disposition = 'rejected' AND rejection_code IS NOT NULL)
        OR (disposition <> 'rejected' AND rejection_code IS NULL)
    )
);

INSERT INTO observation_receipts (
    receive_order, observation_id, adapter_id, entity_id, disposition,
    rejection_code, adapter_received_at, observed_at, expires_at
)
SELECT
    receive_order, observation_id, adapter_id, entity_id, disposition,
    rejection_code, adapter_received_at, observed_at, expires_at
FROM observation_receipts_after_adapter_health;

DROP TABLE observation_receipts_after_adapter_health;
CREATE INDEX observation_receipts_expiry_idx
    ON observation_receipts(expires_at);

CREATE TABLE entity_states (
    entity_id           TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
    observation_id      TEXT NOT NULL UNIQUE REFERENCES observation_receipts(observation_id),
    value_json          TEXT NOT NULL CHECK (json_valid(value_json)),
    adapter_received_at TEXT NOT NULL,
    source_updated_at   TEXT,
    observed_at         TEXT NOT NULL,
    receive_order       INTEGER NOT NULL UNIQUE REFERENCES observation_receipts(receive_order)
);

INSERT INTO entity_states (
    entity_id, observation_id, value_json, adapter_received_at,
    source_updated_at, observed_at, receive_order
)
SELECT
    entity_id, observation_id, value_json, adapter_received_at,
    source_updated_at, observed_at, receive_order
FROM entity_states_after_adapter_health;

DROP TABLE entity_states_after_adapter_health;

DROP INDEX entity_ownership_intervals_history_idx;
DROP INDEX entity_ownership_intervals_one_open_idx;
DROP TABLE entity_ownership_intervals;
DROP INDEX health_transitions_entity_history_idx;
DROP INDEX health_transitions_adapter_history_idx;
DROP TABLE health_transitions;
DROP TABLE entity_availability_current;

UPDATE adapter_instances
SET active_runtime_id = NULL, health_runtime_id = NULL;

DROP INDEX adapter_runtimes_lease_expiry_idx;
DROP INDEX adapter_runtimes_adapter_claimed_idx;
DROP INDEX adapter_runtimes_one_active_idx;
DROP TABLE adapter_runtimes;
DROP TABLE adapter_instances;
