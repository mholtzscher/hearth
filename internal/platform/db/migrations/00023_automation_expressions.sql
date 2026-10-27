-- +goose Up
-- Core executes this script through its version 23 Go migration, after validating
-- all source revisions in the same transaction. A standalone SQL runner is unsafe.
-- Temporarily save dependent rows before removing parents; foreign keys stay on.
CREATE TEMP TABLE expression_saved_heads AS SELECT * FROM automations;
CREATE TEMP TABLE expression_saved_history AS SELECT * FROM automation_history;
CREATE TEMP TABLE expression_saved_executions AS SELECT * FROM automation_step_executions;
CREATE TEMP TABLE expression_saved_holds AS SELECT * FROM automation_holds;
DELETE FROM automation_history;
DELETE FROM automations;
DROP VIEW automation_definitions;
DROP TRIGGER automation_schedule_history_insert;
DROP TRIGGER automation_schedule_history_update;
DROP TRIGGER automation_definition_revision_immutable;

CREATE TABLE automation_definition_revisions_new (
    automation_id TEXT NOT NULL CHECK (length(automation_id) = 40 AND substr(automation_id, 1, 4) = 'aut_'),
    revision INTEGER NOT NULL CHECK (revision >= 1),
    definition_json TEXT NOT NULL CHECK (json_valid(definition_json)
        AND json_type(definition_json) = 'object'
        AND length(CAST(definition_json AS BLOB)) <= 131072),
    PRIMARY KEY (automation_id, revision)
);
INSERT INTO automation_definition_revisions_new SELECT * FROM automation_definition_revisions;
DROP TABLE automation_definition_revisions;
ALTER TABLE automation_definition_revisions_new RENAME TO automation_definition_revisions;

-- Walk only executable positions; vendor objects that resemble instructions are
-- opaque. -> preserves JSON token text rather than coercing integers to REAL.
CREATE TEMP TABLE expression_documents AS
SELECT automation_id AS source, revision AS id, definition_json AS document FROM automation_definition_revisions;
CREATE TEMP TABLE expression_steps AS
WITH RECURSIVE sequences(source, id, path, steps, depth) AS (
    SELECT source, id, '$.steps', document -> '$.steps', 1 FROM expression_documents
    UNION ALL
    SELECT s.source, s.id, s.path || '[' || step.key || '].' || child.key, child.value, s.depth + 1
    FROM sequences AS s, json_each(s.steps) AS step, json_each(step.value) AS child
    WHERE json_extract(step.value, '$.kind') = 'if' AND child.key IN ('then', 'else') AND child.type = 'array'
    UNION ALL
    SELECT s.source, s.id, s.path || '[' || step.key || '].default', step.value -> '$.default', s.depth + 1
    FROM sequences AS s, json_each(s.steps) AS step
    WHERE json_extract(step.value, '$.kind') = 'choose' AND json_type(step.value, '$.default') = 'array'
    UNION ALL
    SELECT s.source, s.id, s.path || '[' || step.key || '].branches[' || branch.key || '].steps', branch.value -> '$.steps', s.depth + 1
    FROM sequences AS s, json_each(s.steps) AS step, json_each(step.value, '$.branches') AS branch
    WHERE json_extract(step.value, '$.kind') = 'choose' AND json_type(branch.value, '$.steps') = 'array'
)
SELECT sequences.source, sequences.id, sequences.path || '[' || step.key || ']' AS path,
    step.value AS document, sequences.depth FROM sequences, json_each(sequences.steps) AS step;
CREATE TEMP TABLE expression_upgrade_validity (valid INTEGER NOT NULL CHECK (valid = 1));
INSERT INTO expression_upgrade_validity SELECT CASE WHEN json_type(document, '$.steps') = 'array'
    AND json_array_length(document, '$.steps') BETWEEN 1 AND 32 THEN 1 ELSE 0 END FROM expression_documents;
INSERT INTO expression_upgrade_validity SELECT CASE WHEN count(*) <= 64 AND max(depth) <= 8
    AND count(DISTINCT json_extract(document, '$.id')) = count(*)
    AND sum(json_extract(document, '$.kind') IN ('command', 'function', 'notification')) <= 32
    THEN 1 ELSE 0 END FROM expression_steps GROUP BY source, id;
INSERT INTO expression_upgrade_validity SELECT CASE WHEN
    (json_extract(document, '$.kind') = 'command' AND json_type(document, '$.parameters') = 'object')
    OR (json_extract(document, '$.kind') = 'function' AND json_type(document, '$.arguments') = 'object')
    OR (json_extract(document, '$.kind') = 'notification' AND json_type(document, '$.title') = 'text' AND json_type(document, '$.message') = 'text')
    OR (json_extract(document, '$.kind') = 'delay' AND json_type(document, '$.duration_ms') = 'integer' AND json_extract(document, '$.duration_ms') BETWEEN 1 AND 86400000)
    OR (json_extract(document, '$.kind') = 'if'
        AND json_type(document, '$.conditions') = 'object'
        AND json_type(document, '$.then') = 'array' AND json_array_length(document, '$.then') BETWEEN 1 AND 32
        AND (json_type(document, '$.else') IS NULL OR (json_type(document, '$.else') = 'array' AND json_array_length(document, '$.else') BETWEEN 1 AND 32)))
    OR (json_extract(document, '$.kind') = 'choose'
        AND json_type(document, '$.branches') = 'array' AND json_array_length(document, '$.branches') BETWEEN 1 AND 32
        AND (json_type(document, '$.default') IS NULL OR (json_type(document, '$.default') = 'array' AND json_array_length(document, '$.default') BETWEEN 1 AND 32)))
    THEN 1 ELSE 0 END FROM expression_steps;
INSERT INTO expression_upgrade_validity SELECT CASE WHEN branch.type = 'object'
    AND json_type(branch.value, '$.id') = 'text'
    AND json_type(branch.value, '$.conditions') = 'object'
    AND json_type(branch.value, '$.steps') = 'array' AND json_array_length(branch.value, '$.steps') BETWEEN 1 AND 32
    THEN 1 ELSE 0 END
FROM expression_steps AS step, json_each(step.document, '$.branches') AS branch WHERE json_extract(step.document, '$.kind') = 'choose';
INSERT INTO expression_upgrade_validity SELECT CASE WHEN count(*) = count(DISTINCT json_extract(branch.value, '$.id'))
    THEN 1 ELSE 0 END FROM expression_steps AS step, json_each(step.document, '$.branches') AS branch
    WHERE json_extract(step.document, '$.kind') = 'choose' GROUP BY step.source, step.id, step.path;
INSERT INTO expression_upgrade_validity SELECT CASE WHEN count(*) = count(DISTINCT json_extract(trigger.value, '$.id'))
    THEN 1 ELSE 0 END FROM expression_documents AS definition, json_each(definition.document, '$.triggers') AS trigger
    GROUP BY definition.source, definition.id;
CREATE TEMP TABLE expression_changes AS
SELECT source, id, path || '.' || field AS path,
    row_number() OVER (PARTITION BY source, id ORDER BY path, field) AS ordinal
FROM expression_steps, (SELECT 'parameters' AS field UNION ALL SELECT 'arguments' UNION ALL SELECT 'title' UNION ALL SELECT 'message' UNION ALL SELECT 'duration_ms') AS fields
WHERE (json_extract(document, '$.kind') = 'command' AND field = 'parameters')
    OR (json_extract(document, '$.kind') = 'function' AND field = 'arguments')
    OR (json_extract(document, '$.kind') = 'notification' AND field IN ('title', 'message'))
    OR (json_extract(document, '$.kind') = 'delay' AND field = 'duration_ms');
CREATE TEMP TABLE expression_upgrades AS
WITH RECURSIVE rewrites(source, id, ordinal, document) AS (
    SELECT source, id, 0, document FROM expression_documents
    UNION ALL
    SELECT r.source, r.id, c.ordinal, json_set(r.document, c.path, json('{"literal":' || (r.document -> c.path) || '}'))
    FROM rewrites AS r JOIN expression_changes AS c ON c.source = r.source AND c.id = r.id AND c.ordinal = r.ordinal + 1
)
SELECT source, id, document FROM rewrites WHERE NOT EXISTS (
    SELECT 1 FROM expression_changes AS c WHERE c.source = rewrites.source AND c.id = rewrites.id AND c.ordinal = rewrites.ordinal + 1);
UPDATE automation_definition_revisions SET definition_json = (
    SELECT document FROM expression_upgrades
    WHERE source = automation_definition_revisions.automation_id AND id = automation_definition_revisions.revision);
DROP TABLE expression_upgrades;
DROP TABLE expression_changes;
DROP TABLE expression_upgrade_validity;
DROP TABLE expression_steps;
DROP TABLE expression_documents;

INSERT INTO automations SELECT * FROM expression_saved_heads;
INSERT INTO automation_history SELECT * FROM expression_saved_history;
INSERT INTO automation_step_executions SELECT * FROM expression_saved_executions;
INSERT INTO automation_holds SELECT * FROM expression_saved_holds;
DROP TABLE expression_saved_heads;
DROP TABLE expression_saved_history;
DROP TABLE expression_saved_executions;
DROP TABLE expression_saved_holds;

CREATE VIEW automation_definitions AS
    SELECT head.id, head.revision, artifact.definition_json, head.created_at, head.updated_at
    FROM automations AS head JOIN automation_definition_revisions AS artifact
      ON artifact.automation_id = head.id AND artifact.revision = head.revision;
-- +goose StatementBegin
CREATE TRIGGER automation_definition_revision_immutable BEFORE UPDATE ON automation_definition_revisions
BEGIN
    SELECT RAISE(ABORT, 'Automation definition revisions are immutable');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER automation_schedule_history_insert BEFORE INSERT ON automation_history
WHEN COALESCE(NEW.run_source, NEW.skip_source) = 'schedule'
BEGIN
    SELECT RAISE(ABORT, 'schedule history requires Cron Trigger provenance') WHERE
        (NEW.kind = 'skip' AND EXISTS (SELECT 1 FROM json_each(NEW.skip_matched_triggers_json)
            WHERE type <> 'object' OR json_extract(value, '$.kind') IS NOT 'cron'))
        OR (NEW.kind = 'run' AND EXISTS (SELECT 1 FROM json_each(NEW.run_matched_trigger_ids_json) AS matched
            WHERE matched.type <> 'text' OR NOT EXISTS (SELECT 1 FROM automation_definition_revisions AS artifact,
                json_each(artifact.definition_json, '$.triggers') AS trigger
                WHERE artifact.automation_id = NEW.automation_id AND artifact.revision = NEW.definition_revision
                    AND json_extract(trigger.value, '$.id') = matched.value AND json_extract(trigger.value, '$.kind') = 'cron')));
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER automation_schedule_history_update BEFORE UPDATE ON automation_history
WHEN COALESCE(NEW.run_source, NEW.skip_source) = 'schedule'
BEGIN
    SELECT RAISE(ABORT, 'schedule history requires Cron Trigger provenance') WHERE
        (NEW.kind = 'skip' AND EXISTS (SELECT 1 FROM json_each(NEW.skip_matched_triggers_json)
            WHERE type <> 'object' OR json_extract(value, '$.kind') IS NOT 'cron'))
        OR (NEW.kind = 'run' AND EXISTS (SELECT 1 FROM json_each(NEW.run_matched_trigger_ids_json) AS matched
            WHERE matched.type <> 'text' OR NOT EXISTS (SELECT 1 FROM automation_definition_revisions AS artifact,
                json_each(artifact.definition_json, '$.triggers') AS trigger
                WHERE artifact.automation_id = NEW.automation_id AND artifact.revision = NEW.definition_revision
                    AND json_extract(trigger.value, '$.id') = matched.value AND json_extract(trigger.value, '$.kind') = 'cron')));
END;
-- +goose StatementEnd

ALTER TABLE automation_history ADD COLUMN data_evidence TEXT NOT NULL DEFAULT 'legacy_unavailable'
    CHECK (data_evidence IN ('recorded', 'legacy_unavailable'));
ALTER TABLE automation_history ADD COLUMN value_initialization_json TEXT
    CHECK (value_initialization_json IS NULL OR (json_valid(value_initialization_json)
        AND json_type(value_initialization_json) = 'object' AND length(CAST(value_initialization_json AS BLOB)) <= 131072));
ALTER TABLE automation_history ADD COLUMN value_evidence_bytes INTEGER NOT NULL DEFAULT 0
    CHECK (value_evidence_bytes BETWEEN 0 AND 2097152);
ALTER TABLE automation_history ADD COLUMN value_evidence_records INTEGER NOT NULL DEFAULT 0
    CHECK (value_evidence_records BETWEEN 0 AND 256);

CREATE TABLE automation_step_executions_new (
    run_id TEXT NOT NULL REFERENCES automation_history(id) ON DELETE CASCADE
        CHECK (length(run_id) = 40 AND substr(run_id, 1, 4) = 'arn_'),
    step_id TEXT NOT NULL CHECK (length(step_id) BETWEEN 1 AND 63
        AND substr(step_id, 1, 1) GLOB '[a-z0-9]' AND step_id NOT GLOB '*[^a-z0-9_-]*'),
    sequence INTEGER NOT NULL CHECK (sequence BETWEEN 0 AND 63),
    kind TEXT NOT NULL CHECK (kind IN ('command', 'delay', 'notification', 'function', 'if', 'choose', 'read_state', 'set_variables')),
    phase TEXT NOT NULL CHECK (phase IN ('running', 'succeeded', 'failed', 'interrupted')),
    started_at TEXT NOT NULL,
    completed_at TEXT,
    evidence_json TEXT NOT NULL CHECK (json_valid(evidence_json)
        AND json_type(evidence_json) = 'object'),
    PRIMARY KEY (run_id, step_id), UNIQUE (run_id, sequence),
    CHECK ((phase = 'running') = (completed_at IS NULL)),
    CHECK ((kind IN ('command', 'notification', 'function') AND phase IN ('running', 'succeeded', 'failed', 'interrupted'))
        OR (kind = 'delay' AND phase IN ('running', 'succeeded', 'interrupted'))
        OR (kind IN ('if', 'choose', 'read_state', 'set_variables') AND phase IN ('succeeded', 'failed') AND completed_at = started_at)),
    CHECK (json_type(evidence_json, '$.value_failure') IS NULL OR (phase = 'failed'
        AND json_type(evidence_json, '$.value_failure') IS 'object'
        AND json_type(evidence_json, '$.value_failure.failure_code') IS 'text')),
    CHECK (kind <> 'notification' OR (phase = 'failed' AND json_type(evidence_json, '$.value_failure') IS 'object'
        AND json_type(evidence_json, '$.attempt_id') IS NULL)
        OR (json_type(evidence_json, '$.attempt_id') IS 'text' AND length(json_extract(evidence_json, '$.attempt_id')) = 40
            AND substr(json_extract(evidence_json, '$.attempt_id'), 1, 4) = 'ntf_')),
    CHECK (kind <> 'function' OR (phase = 'failed' AND json_type(evidence_json, '$.value_failure') IS 'object'
        AND json_type(evidence_json, '$.invocation_id') IS NULL)
        OR (json_type(evidence_json, '$.invocation_id') IS 'text' AND length(json_extract(evidence_json, '$.invocation_id')) = 40
            AND substr(json_extract(evidence_json, '$.invocation_id'), 1, 4) = 'afi_')),
    CHECK (kind <> 'command' OR (
        (phase = 'interrupted' AND json_type(evidence_json, '$.command_id') IS NULL
            AND json_type(evidence_json, '$.correlation_id') IS NULL AND json_type(evidence_json, '$.verified_command_id') IS NULL)
        OR (phase = 'failed' AND json_type(evidence_json, '$.value_failure') IS 'object'
            AND json_type(evidence_json, '$.command_id') IS NULL AND json_type(evidence_json, '$.correlation_id') IS NULL
            AND json_type(evidence_json, '$.verified_command_id') IS NULL)
        OR (json_type(evidence_json, '$.command_id') IS 'text' AND length(json_extract(evidence_json, '$.command_id')) = 40
            AND substr(json_extract(evidence_json, '$.command_id'), 1, 4) = 'cmd_'
            AND json_type(evidence_json, '$.correlation_id') IS 'text' AND length(json_extract(evidence_json, '$.correlation_id')) = 40
            AND substr(json_extract(evidence_json, '$.correlation_id'), 1, 4) = 'cor_'))),
    CHECK (kind <> 'command' OR phase <> 'succeeded' OR (json_type(evidence_json, '$.verified_command_id') IS 'text'
        AND json_extract(evidence_json, '$.verified_command_id') = json_extract(evidence_json, '$.command_id')
        AND COALESCE(json_extract(evidence_json, '$.command_outcome') IN ('satisfied', 'dispatched'), 0))),
    CHECK (phase NOT IN ('failed', 'interrupted') OR json_type(evidence_json, '$.value_failure') IS 'object'
        OR (kind IN ('if', 'choose') AND json_type(evidence_json, '$.failure_code') IS 'text')
        OR (kind NOT IN ('if', 'choose') AND json_type(evidence_json, '$.reason_code') IS 'text'
            AND length(json_extract(evidence_json, '$.reason_code')) > 0)),
    CHECK (kind <> 'notification' OR phase <> 'succeeded' OR json_extract(evidence_json, '$.outcome') IS 'accepted'),
    CHECK (kind <> 'function' OR phase <> 'succeeded' OR (json_type(evidence_json, '$.catalog_revision') IS 'text'
        AND json_extract(evidence_json, '$.invocation_outcome') IS 'succeeded')),
    CHECK (kind NOT IN ('if', 'choose') OR (
        (phase = 'succeeded' AND COALESCE(json_extract(evidence_json, '$.outcome') IN ('then', 'else', 'branch', 'default', 'no_match'), 0))
        OR (phase = 'failed' AND COALESCE(json_extract(evidence_json, '$.outcome') IN ('unknown', 'error'), 0))))
);
INSERT INTO automation_step_executions_new SELECT * FROM automation_step_executions;
DROP TABLE automation_step_executions;
ALTER TABLE automation_step_executions_new RENAME TO automation_step_executions;
CREATE UNIQUE INDEX automation_step_executions_one_running_idx ON automation_step_executions(run_id) WHERE phase = 'running';
CREATE UNIQUE INDEX automation_step_executions_notification_attempt_idx ON automation_step_executions(json_extract(evidence_json, '$.attempt_id')) WHERE kind = 'notification';
CREATE UNIQUE INDEX automation_step_executions_function_invocation_idx ON automation_step_executions(json_extract(evidence_json, '$.invocation_id')) WHERE kind = 'function';

-- +goose Down
CREATE TEMP TABLE automation_expression_rollback_guard (id INTEGER);
-- +goose StatementBegin
CREATE TEMP TRIGGER automation_expression_rollback_abort BEFORE INSERT ON automation_expression_rollback_guard
BEGIN
    SELECT RAISE(ABORT, 'Restore the pre-upgrade backup with its matching binary; expressions have no lossless Down migration');
END;
-- +goose StatementEnd
INSERT INTO automation_expression_rollback_guard VALUES (1);
