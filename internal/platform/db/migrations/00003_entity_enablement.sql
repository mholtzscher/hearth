-- +goose Up
PRAGMA foreign_keys = ON;

ALTER TABLE entities
ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1));

DROP INDEX commands_entity_requested_idx;
ALTER TABLE commands RENAME TO commands_before_entity_enablement;

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
    status, requested_at, deadline_at, accepted_at, completed_at,
    outcome_observation_id, failure_code
FROM commands_before_entity_enablement;

DROP TABLE commands_before_entity_enablement;
CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC, id DESC);

CREATE TABLE entity_states_before_entity_enablement AS
SELECT entity_id, observation_id, value_json, adapter_received_at,
       source_updated_at, observed_at, receive_order
FROM entity_states;
DROP TABLE entity_states;

DROP INDEX observation_receipts_expiry_idx;
ALTER TABLE observation_receipts RENAME TO observation_receipts_before_entity_enablement;

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
FROM observation_receipts_before_entity_enablement;

DROP TABLE observation_receipts_before_entity_enablement;
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
FROM entity_states_before_entity_enablement;

DROP TABLE entity_states_before_entity_enablement;

-- +goose Down
PRAGMA foreign_keys = ON;

UPDATE commands
SET status = 'internal_failure', failure_code = 'internal_error'
WHERE status = 'entity_disabled';

DELETE FROM observation_receipts
WHERE rejection_code = 'entity_disabled';

CREATE TABLE entity_states_after_entity_enablement AS
SELECT entity_id, observation_id, value_json, adapter_received_at,
       source_updated_at, observed_at, receive_order
FROM entity_states;
DROP TABLE entity_states;

DROP INDEX observation_receipts_expiry_idx;
ALTER TABLE observation_receipts RENAME TO observation_receipts_after_entity_enablement;

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
            'unknown_entity', 'wrong_adapter', 'invalid_value'
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
FROM observation_receipts_after_entity_enablement;

DROP TABLE observation_receipts_after_entity_enablement;
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
FROM entity_states_after_entity_enablement;

DROP TABLE entity_states_after_entity_enablement;

DROP INDEX commands_entity_requested_idx;
ALTER TABLE commands RENAME TO commands_after_entity_enablement;

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
            'adapter_unavailable', 'outcome_timeout',
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
            'internal_error', 'core_restarted'
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
    status, requested_at, deadline_at, accepted_at, completed_at,
    outcome_observation_id, failure_code
FROM commands_after_entity_enablement;

DROP TABLE commands_after_entity_enablement;
CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC, id DESC);

ALTER TABLE entities DROP COLUMN enabled;
