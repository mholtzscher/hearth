-- +goose Up
-- Rebuild bounded parents with foreign keys enabled, preserving all children.
CREATE TABLE automations_new (
    id TEXT PRIMARY KEY CHECK (length(id) = 40 AND substr(id, 1, 4) = 'aut_'),
    revision INTEGER NOT NULL CHECK (revision >= 1),
    definition_json TEXT NOT NULL CHECK (json_valid(definition_json)
        AND json_type(definition_json) = 'object'
        AND length(CAST(definition_json AS BLOB)) <= 131072),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO automations_new SELECT * FROM automations;
CREATE TEMP TABLE expression_holds_backup AS SELECT * FROM automation_holds;
DELETE FROM automation_holds;
DROP TABLE automations;
ALTER TABLE automations_new RENAME TO automations;
INSERT INTO automation_holds SELECT * FROM expression_holds_backup;
DROP TABLE expression_holds_backup;

CREATE TABLE automation_history_new (
    id TEXT PRIMARY KEY CHECK (
        (length(id) = 40 AND substr(id, 1, 4) = 'arn_')
        OR (length(id) = 40 AND substr(id, 1, 4) = 'ask_')),
    automation_id TEXT NOT NULL CHECK (length(automation_id) = 40 AND substr(automation_id, 1, 4) = 'aut_'),
    automation_name TEXT NOT NULL CHECK (length(automation_name) BETWEEN 1 AND 200),
    kind TEXT NOT NULL CHECK (kind IN ('run', 'skip')),
    revision INTEGER NOT NULL CHECK (revision >= 1),
    recorded_at TEXT NOT NULL,
    fact_id TEXT CHECK (fact_id IS NULL OR (length(fact_id) = 40 AND substr(fact_id, 1, 4) = 'fct_')),
    fact_family TEXT CHECK (fact_family IS NULL OR fact_family IN ('observation', 'entity_event')),
    fact_entity_id TEXT CHECK (fact_entity_id IS NULL OR (length(fact_entity_id) = 40 AND substr(fact_entity_id, 1, 4) = 'ent_')),
    fact_variant TEXT,
    fact_causation_id TEXT,
    fact_value_json TEXT CHECK (fact_value_json IS NULL OR json_valid(fact_value_json)),
    fact_emitted_at TEXT,
    fact_previous_value_json TEXT CHECK (fact_previous_value_json IS NULL OR json_valid(fact_previous_value_json)),
    run_snapshot_json TEXT CHECK (run_snapshot_json IS NULL OR (
        json_valid(run_snapshot_json) AND json_type(run_snapshot_json) = 'object'
        AND length(CAST(run_snapshot_json AS BLOB)) <= 131072)),
    run_source TEXT CHECK (run_source IS NULL OR run_source IN ('device_fact', 'manual', 'held_state', 'schedule')),
    run_status TEXT CHECK (run_status IS NULL OR run_status IN ('running', 'succeeded', 'failed', 'interrupted')),
    run_failure_code TEXT,
    run_started_at TEXT,
    run_completed_at TEXT,
    run_matched_trigger_ids_json TEXT CHECK (run_matched_trigger_ids_json IS NULL OR (
        json_valid(run_matched_trigger_ids_json) AND json_type(run_matched_trigger_ids_json) = 'array')),
    skip_matched_triggers_json TEXT CHECK (skip_matched_triggers_json IS NULL OR (
        json_valid(skip_matched_triggers_json) AND json_type(skip_matched_triggers_json) = 'array')),
    skip_reason TEXT CHECK (skip_reason IS NULL OR skip_reason IN (
        'automation_busy', 'stale_fact', 'conditions_false', 'conditions_unknown')),
    skip_source TEXT CHECK (skip_source IS NULL OR skip_source IN ('device_fact', 'manual', 'held_state', 'schedule')),
    hold_trigger_id TEXT CHECK (hold_trigger_id IS NULL OR (length(hold_trigger_id) BETWEEN 1 AND 63
        AND substr(hold_trigger_id, 1, 1) GLOB '[a-z0-9]' AND hold_trigger_id NOT GLOB '*[^a-z0-9_-]*')),
    hold_started_at TEXT,
    hold_due_at TEXT,
    condition_decision_json TEXT NOT NULL CHECK (json_valid(condition_decision_json) AND json_type(condition_decision_json) = 'object'),
    condition_mode TEXT NOT NULL DEFAULT 'not_configured' CHECK (condition_mode IN ('not_configured', 'not_evaluated', 'bypassed', 'evaluated')),
    condition_bypassed INTEGER NOT NULL DEFAULT 0 CHECK (condition_bypassed IN (0, 1)),
    condition_result TEXT CHECK (condition_result IS NULL OR condition_result IN ('true', 'false', 'unknown')),
    data_evidence TEXT NOT NULL DEFAULT 'legacy_unavailable' CHECK (data_evidence IN ('recorded', 'legacy_unavailable')),
    CHECK (
        (fact_id IS NULL AND fact_family IS NULL AND fact_entity_id IS NULL AND fact_variant IS NULL
            AND fact_causation_id IS NULL AND fact_value_json IS NULL AND fact_emitted_at IS NULL)
        OR (fact_id IS NOT NULL AND fact_family IS NOT NULL AND fact_entity_id IS NOT NULL
            AND fact_variant IS NOT NULL AND fact_causation_id IS NOT NULL AND fact_emitted_at IS NOT NULL
            AND (fact_family <> 'observation' OR fact_value_json IS NOT NULL)
            AND (fact_family <> 'entity_event' OR fact_value_json IS NULL))),
    CHECK ((hold_trigger_id IS NULL AND hold_started_at IS NULL AND hold_due_at IS NULL)
        OR (hold_trigger_id IS NOT NULL AND hold_started_at IS NOT NULL AND hold_due_at IS NOT NULL AND hold_due_at > hold_started_at)),
    CHECK (
        (kind = 'run' AND run_snapshot_json IS NOT NULL AND run_source IS NOT NULL
            AND run_status IS NOT NULL AND run_started_at IS NOT NULL AND run_matched_trigger_ids_json IS NOT NULL
            AND skip_matched_triggers_json IS NULL AND skip_reason IS NULL AND skip_source IS NULL
            AND (run_status = 'running') = (run_completed_at IS NULL)
            AND (run_status IN ('failed', 'interrupted')) = (run_failure_code IS NOT NULL)
            AND ((run_source = 'device_fact' AND fact_id IS NOT NULL AND hold_trigger_id IS NULL)
                OR (run_source = 'manual' AND fact_id IS NULL AND hold_trigger_id IS NULL)
                OR (run_source = 'held_state' AND fact_id IS NULL AND hold_trigger_id IS NOT NULL AND json_array_length(run_matched_trigger_ids_json) = 1)
                OR (run_source = 'schedule' AND fact_id IS NULL AND hold_trigger_id IS NULL AND json_array_length(run_matched_trigger_ids_json) BETWEEN 1 AND 32)))
        OR (kind = 'skip' AND skip_matched_triggers_json IS NOT NULL AND skip_reason IS NOT NULL AND skip_source IS NOT NULL
            AND ((skip_source = 'device_fact' AND fact_id IS NOT NULL AND hold_trigger_id IS NULL AND json_array_length(skip_matched_triggers_json) > 0)
                OR (skip_source = 'manual' AND fact_id IS NULL AND hold_trigger_id IS NULL AND json_array_length(skip_matched_triggers_json) = 0 AND skip_reason IN ('conditions_false', 'conditions_unknown'))
                OR (skip_source = 'held_state' AND fact_id IS NULL AND hold_trigger_id IS NOT NULL AND json_array_length(skip_matched_triggers_json) = 1 AND skip_reason IN ('automation_busy', 'conditions_false', 'conditions_unknown'))
                OR (skip_source = 'schedule' AND fact_id IS NULL AND hold_trigger_id IS NULL AND json_array_length(skip_matched_triggers_json) BETWEEN 1 AND 32 AND skip_reason IN ('automation_busy', 'conditions_false', 'conditions_unknown')))
            AND run_snapshot_json IS NULL AND run_source IS NULL AND run_status IS NULL AND run_failure_code IS NULL
            AND run_started_at IS NULL AND run_completed_at IS NULL AND run_matched_trigger_ids_json IS NULL)),
    CHECK (COALESCE(run_source, skip_source) <> 'schedule' OR (
        fact_previous_value_json IS NULL AND condition_bypassed = 0
        AND json_extract(condition_decision_json, '$.bypass_requested') IS 0
        AND json_extract(condition_decision_json, '$.mode') IS condition_mode
        AND condition_mode IN ('not_configured', 'not_evaluated', 'evaluated')
        AND ((kind = 'run' AND ((condition_mode = 'not_configured' AND condition_result IS NULL)
                OR (condition_mode = 'evaluated' AND condition_result IS NOT NULL AND condition_result = 'true')))
            OR (kind = 'skip' AND ((skip_reason = 'automation_busy' AND condition_mode IN ('not_configured', 'not_evaluated') AND condition_result IS NULL)
                OR (skip_reason = 'conditions_false' AND condition_mode = 'evaluated' AND condition_result IS NOT NULL AND condition_result = 'false')
                OR (skip_reason = 'conditions_unknown' AND condition_mode = 'evaluated' AND condition_result IS NOT NULL AND condition_result = 'unknown'))))))
);
INSERT INTO automation_history_new SELECT *, 'legacy_unavailable' FROM automation_history;
CREATE TEMP TABLE expression_steps_backup AS SELECT * FROM automation_run_steps;
CREATE TEMP TABLE expression_branches_backup AS SELECT * FROM automation_run_branch_decisions;
CREATE TEMP TABLE expression_delays_backup AS SELECT * FROM automation_run_delays;
CREATE TEMP TABLE expression_notifications_backup AS SELECT * FROM automation_run_notifications;
CREATE TEMP TABLE expression_functions_backup AS SELECT * FROM automation_run_functions;
DELETE FROM automation_run_steps;
DELETE FROM automation_run_branch_decisions;
DELETE FROM automation_run_delays;
DELETE FROM automation_run_notifications;
DELETE FROM automation_run_functions;
DROP TABLE automation_history;
ALTER TABLE automation_history_new RENAME TO automation_history;
INSERT INTO automation_run_steps SELECT * FROM expression_steps_backup;
INSERT INTO automation_run_branch_decisions SELECT * FROM expression_branches_backup;
INSERT INTO automation_run_delays SELECT * FROM expression_delays_backup;
INSERT INTO automation_run_notifications SELECT * FROM expression_notifications_backup;
INSERT INTO automation_run_functions SELECT * FROM expression_functions_backup;
DROP TABLE expression_steps_backup;
DROP TABLE expression_branches_backup;
DROP TABLE expression_delays_backup;
DROP TABLE expression_notifications_backup;
DROP TABLE expression_functions_backup;
CREATE INDEX automation_history_page_idx ON automation_history(automation_id, recorded_at DESC, id DESC);
CREATE UNIQUE INDEX automation_history_one_running_run_idx ON automation_history(automation_id) WHERE kind = 'run' AND run_status = 'running';
CREATE UNIQUE INDEX automation_history_fact_outcome_idx ON automation_history(fact_id, automation_id) WHERE fact_id IS NOT NULL;

-- +goose StatementBegin
CREATE TRIGGER automation_schedule_history_insert BEFORE INSERT ON automation_history
WHEN COALESCE(NEW.run_source, NEW.skip_source) = 'schedule'
BEGIN
    SELECT RAISE(ABORT, 'schedule history requires Cron Trigger provenance') WHERE
        (NEW.kind = 'skip' AND EXISTS (SELECT 1 FROM json_each(NEW.skip_matched_triggers_json)
            WHERE type <> 'object' OR json_extract(value, '$.kind') IS NOT 'cron'))
        OR (NEW.kind = 'run' AND EXISTS (SELECT 1 FROM json_each(NEW.run_matched_trigger_ids_json) AS matched
            WHERE matched.type <> 'text' OR NOT EXISTS (SELECT 1 FROM json_each(NEW.run_snapshot_json, '$.triggers') AS trigger
                WHERE json_extract(trigger.value, '$.id') = matched.value AND json_extract(trigger.value, '$.kind') = 'cron')));
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
            WHERE matched.type <> 'text' OR NOT EXISTS (SELECT 1 FROM json_each(NEW.run_snapshot_json, '$.triggers') AS trigger
                WHERE json_extract(trigger.value, '$.id') = matched.value AND json_extract(trigger.value, '$.kind') = 'cron')));
END;
-- +goose StatementEnd

-- Enumerate executable positions only. The -> operator returns JSON text rather
-- than a coerced SQL numeric value, preserving arbitrary vendor integer tokens.
CREATE TEMP TABLE expression_documents AS
SELECT 'definition' AS source, id, definition_json AS document FROM automations
UNION ALL SELECT 'snapshot', id, run_snapshot_json FROM automation_history WHERE run_snapshot_json IS NOT NULL;
CREATE TEMP TABLE expression_steps AS
WITH RECURSIVE sequences(source, id, path, steps, depth) AS (
    SELECT source, id, '$.steps', document -> '$.steps', 1 FROM expression_documents
    UNION ALL
    SELECT s.source, s.id, s.path || '[' || step.key || '].' || child.key, child.value, s.depth + 1
    FROM sequences AS s, json_each(s.steps) AS step, json_each(step.value) AS child
    WHERE json_extract(step.value, '$.kind') IN ('if', 'choose') AND child.key IN ('then', 'else', 'default') AND child.type = 'array'
    UNION ALL
    SELECT s.source, s.id, s.path || '[' || step.key || '].branches[' || branch.key || '].steps', branch.value -> '$.steps', s.depth + 1
    FROM sequences AS s, json_each(s.steps) AS step, json_each(step.value, '$.branches') AS branch
    WHERE json_extract(step.value, '$.kind') = 'choose' AND json_type(branch.value, '$.steps') = 'array'
)
SELECT sequences.source, sequences.id, sequences.path || '[' || step.key || ']' AS path, step.value AS document, sequences.depth FROM sequences, json_each(sequences.steps) AS step;
CREATE TEMP TABLE expression_upgrade_validity (valid INTEGER NOT NULL CHECK (valid = 1));
INSERT INTO expression_upgrade_validity SELECT CASE WHEN json_type(document, '$.steps') = 'array' AND json_array_length(document, '$.steps') BETWEEN 1 AND 32 THEN 1 ELSE 0 END FROM expression_documents;
INSERT INTO expression_upgrade_validity SELECT CASE WHEN count(*) <= 64 AND max(depth) <= 8
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
UPDATE automations SET definition_json = (SELECT document FROM expression_upgrades WHERE source = 'definition' AND id = automations.id);
UPDATE automation_history SET run_snapshot_json = (SELECT document FROM expression_upgrades WHERE source = 'snapshot' AND id = automation_history.id) WHERE run_snapshot_json IS NOT NULL;
DROP TABLE expression_upgrades;
DROP TABLE expression_changes;
DROP TABLE expression_upgrade_validity;
DROP TABLE expression_steps;
DROP TABLE expression_documents;

CREATE TABLE automation_run_data (
    run_id TEXT NOT NULL REFERENCES automation_history(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence BETWEEN 0 AND 255),
    step_id TEXT,
    kind TEXT NOT NULL CHECK (kind IN ('variables_initialized', 'inputs_resolved', 'variables_assigned', 'state_captured', 'function_output', 'failed')),
    recorded_at TEXT NOT NULL,
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json) AND json_type(payload_json) = 'object' AND length(CAST(payload_json AS BLOB)) <= 131072),
    PRIMARY KEY (run_id, sequence)
);
CREATE UNIQUE INDEX automation_run_data_phase_idx ON automation_run_data(run_id, step_id, kind) WHERE step_id IS NOT NULL;

-- +goose Down
-- Deliberately fail: expressions and retained values cannot be downgraded losslessly.
SELECT json('Automation expressions require restoring a pre-upgrade backup with the previous binary');
