-- +goose Up
-- Intentional Automation-only development cutover. Keep every other module's
-- records and the revision-sharing/history-index layout intact.
DELETE FROM automation_fact_receipts;
DELETE FROM automation_schedule_watermarks;
DELETE FROM automation_history;
DELETE FROM automations;
DELETE FROM automation_definition_revisions;

DROP TABLE automation_run_steps;
DROP TABLE automation_run_branch_decisions;
DROP TABLE automation_run_delays;
DROP TABLE automation_run_notifications;
DROP TABLE automation_run_functions;

CREATE TABLE automation_step_executions (
    run_id TEXT NOT NULL REFERENCES automation_history(id) ON DELETE CASCADE
        CHECK (length(run_id) = 40 AND substr(run_id, 1, 4) = 'arn_'),
    step_id TEXT NOT NULL CHECK (length(step_id) BETWEEN 1 AND 63
        AND substr(step_id, 1, 1) GLOB '[a-z0-9]' AND step_id NOT GLOB '*[^a-z0-9_-]*'),
    sequence INTEGER NOT NULL CHECK (sequence BETWEEN 0 AND 63),
    kind TEXT NOT NULL CHECK (kind IN ('command', 'delay', 'notification', 'function', 'if', 'choose')),
    phase TEXT NOT NULL CHECK (phase IN ('running', 'succeeded', 'failed', 'interrupted')),
    started_at TEXT NOT NULL,
    completed_at TEXT,
    evidence_json TEXT NOT NULL CHECK (json_valid(evidence_json)
        AND json_type(evidence_json) = 'object'),
    PRIMARY KEY (run_id, step_id),
    UNIQUE (run_id, sequence),
    CHECK ((phase = 'running') = (completed_at IS NULL)),
    CHECK (
        (kind IN ('command', 'notification', 'function') AND phase IN ('running', 'succeeded', 'failed', 'interrupted'))
        OR (kind = 'delay' AND phase IN ('running', 'succeeded', 'interrupted'))
        OR (kind IN ('if', 'choose') AND phase IN ('succeeded', 'failed') AND completed_at = started_at)
    ),
    CHECK (kind <> 'notification' OR (json_type(evidence_json, '$.attempt_id') IS 'text'
        AND length(json_extract(evidence_json, '$.attempt_id')) = 40
        AND substr(json_extract(evidence_json, '$.attempt_id'), 1, 4) = 'ntf_')),
    CHECK (kind <> 'function' OR (json_type(evidence_json, '$.invocation_id') IS 'text'
        AND length(json_extract(evidence_json, '$.invocation_id')) = 40
        AND substr(json_extract(evidence_json, '$.invocation_id'), 1, 4) = 'afi_')),
    CHECK (kind <> 'command' OR (
        (phase = 'interrupted' AND json_type(evidence_json, '$.command_id') IS NULL
            AND json_type(evidence_json, '$.correlation_id') IS NULL
            AND json_type(evidence_json, '$.verified_command_id') IS NULL)
        OR (json_type(evidence_json, '$.command_id') IS 'text'
            AND length(json_extract(evidence_json, '$.command_id')) = 40
            AND substr(json_extract(evidence_json, '$.command_id'), 1, 4) = 'cmd_'
            AND json_type(evidence_json, '$.correlation_id') IS 'text'
            AND length(json_extract(evidence_json, '$.correlation_id')) = 40
            AND substr(json_extract(evidence_json, '$.correlation_id'), 1, 4) = 'cor_')
    )),
    CHECK (kind <> 'command' OR phase <> 'succeeded' OR (
        json_type(evidence_json, '$.verified_command_id') IS 'text'
        AND json_extract(evidence_json, '$.verified_command_id') = json_extract(evidence_json, '$.command_id')
        AND COALESCE(json_extract(evidence_json, '$.command_outcome') IN ('satisfied', 'dispatched'), 0)
    )),
    CHECK (phase NOT IN ('failed', 'interrupted') OR (
        (kind IN ('if', 'choose') AND json_type(evidence_json, '$.failure_code') IS 'text')
        OR (kind NOT IN ('if', 'choose') AND json_type(evidence_json, '$.reason_code') IS 'text'
            AND length(json_extract(evidence_json, '$.reason_code')) > 0)
    )),
    CHECK (kind <> 'notification' OR phase <> 'succeeded'
        OR json_extract(evidence_json, '$.outcome') IS 'accepted'),
    CHECK (kind <> 'function' OR phase <> 'succeeded' OR (
        json_type(evidence_json, '$.catalog_revision') IS 'text'
        AND json_extract(evidence_json, '$.invocation_outcome') IS 'succeeded'
    )),
    CHECK (kind NOT IN ('if', 'choose') OR (
        (phase = 'succeeded' AND COALESCE(json_extract(evidence_json, '$.outcome') IN ('then', 'else', 'branch', 'default', 'no_match'), 0))
        OR (phase = 'failed' AND COALESCE(json_extract(evidence_json, '$.outcome') IN ('unknown', 'error'), 0))
    ))
);

CREATE UNIQUE INDEX automation_step_executions_one_running_idx
    ON automation_step_executions(run_id) WHERE phase = 'running';
CREATE UNIQUE INDEX automation_step_executions_notification_attempt_idx
    ON automation_step_executions(json_extract(evidence_json, '$.attempt_id')) WHERE kind = 'notification';
CREATE UNIQUE INDEX automation_step_executions_function_invocation_idx
    ON automation_step_executions(json_extract(evidence_json, '$.invocation_id')) WHERE kind = 'function';

-- +goose Down
CREATE TEMP TABLE automation_steps_rollback_guard (id INTEGER);
-- +goose StatementBegin
CREATE TEMP TRIGGER automation_steps_rollback_abort BEFORE INSERT ON automation_steps_rollback_guard
BEGIN
    SELECT RAISE(ABORT, 'Restore the pre-upgrade backup with its matching binary; unified Step executions have no Down migration');
END;
-- +goose StatementEnd
INSERT INTO automation_steps_rollback_guard VALUES (1);
