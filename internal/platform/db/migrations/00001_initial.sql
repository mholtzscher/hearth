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
    updated_at   TEXT NOT NULL
);

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

CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC);

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

-- +goose Down
DROP TABLE entity_states;
DROP INDEX observation_receipts_expiry_idx;
DROP TABLE observation_receipts;
DROP TABLE adapter_entity_mappings;
DROP TABLE adapter_bindings;
DROP INDEX commands_entity_requested_idx;
DROP TABLE commands;
DROP TABLE entities;
DROP TABLE devices;
