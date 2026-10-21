-- +goose Up
-- A legacy definition may occupy all 65536 bytes and contain 32 Command leaves.
-- Each explicit ,"kind":"command" adds 17 bytes: allow exactly 544 more bytes.
-- Rebuild with checks and foreign keys enabled, preserving every child row.
CREATE TABLE automations_new (
    id TEXT PRIMARY KEY CHECK (length(id) = 40 AND substr(id, 1, 4) = 'aut_'),
    revision INTEGER NOT NULL CHECK (revision >= 1),
    definition_json TEXT NOT NULL CHECK (json_valid(definition_json)
        AND json_type(definition_json) = 'object'
        AND length(CAST(definition_json AS BLOB)) <= 66080),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO automations_new SELECT * FROM automations;
CREATE TEMP TABLE command_kind_holds_backup AS SELECT * FROM automation_holds;
DELETE FROM automation_holds;
DROP TABLE automations;
ALTER TABLE automations_new RENAME TO automations;
INSERT INTO automation_holds SELECT * FROM command_kind_holds_backup;
DROP TABLE command_kind_holds_backup;

CREATE TABLE automation_history_new (
    id TEXT PRIMARY KEY CHECK (
        (length(id) = 40 AND substr(id, 1, 4) = 'arn_')
        OR (length(id) = 40 AND substr(id, 1, 4) = 'ask_')),
    automation_id TEXT NOT NULL CHECK (
        length(automation_id) = 40 AND substr(automation_id, 1, 4) = 'aut_'),
    automation_name TEXT NOT NULL CHECK (length(automation_name) BETWEEN 1 AND 200),
    kind TEXT NOT NULL CHECK (kind IN ('run', 'skip')),
    revision INTEGER NOT NULL CHECK (revision >= 1),
    recorded_at TEXT NOT NULL,
    fact_id TEXT CHECK (fact_id IS NULL OR (length(fact_id) = 40 AND substr(fact_id, 1, 4) = 'fct_')),
    fact_family TEXT CHECK (fact_family IS NULL OR fact_family IN ('observation', 'entity_event')),
    fact_entity_id TEXT CHECK (
        fact_entity_id IS NULL OR (length(fact_entity_id) = 40 AND substr(fact_entity_id, 1, 4) = 'ent_')),
    fact_variant TEXT,
    fact_causation_id TEXT,
    fact_value_json TEXT CHECK (fact_value_json IS NULL OR json_valid(fact_value_json)),
    fact_emitted_at TEXT,
    fact_previous_value_json TEXT CHECK (fact_previous_value_json IS NULL OR json_valid(fact_previous_value_json)),
    run_snapshot_json TEXT CHECK (run_snapshot_json IS NULL OR (
        json_valid(run_snapshot_json) AND json_type(run_snapshot_json) = 'object'
        AND length(CAST(run_snapshot_json AS BLOB)) <= 66080)),
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
    condition_decision_json TEXT NOT NULL CHECK (
        json_valid(condition_decision_json) AND json_type(condition_decision_json) = 'object'),
    condition_mode TEXT NOT NULL DEFAULT 'not_configured'
        CHECK (condition_mode IN ('not_configured', 'not_evaluated', 'bypassed', 'evaluated')),
    condition_bypassed INTEGER NOT NULL DEFAULT 0 CHECK (condition_bypassed IN (0, 1)),
    condition_result TEXT CHECK (condition_result IS NULL OR condition_result IN ('true', 'false', 'unknown')),
    CHECK (
        (fact_id IS NULL AND fact_family IS NULL AND fact_entity_id IS NULL AND fact_variant IS NULL
            AND fact_causation_id IS NULL AND fact_value_json IS NULL AND fact_emitted_at IS NULL)
        OR (fact_id IS NOT NULL AND fact_family IS NOT NULL AND fact_entity_id IS NOT NULL
            AND fact_variant IS NOT NULL AND fact_causation_id IS NOT NULL AND fact_emitted_at IS NOT NULL
            AND (fact_family <> 'observation' OR fact_value_json IS NOT NULL)
            AND (fact_family <> 'entity_event' OR fact_value_json IS NULL))),
    CHECK ((hold_trigger_id IS NULL AND hold_started_at IS NULL AND hold_due_at IS NULL)
        OR (hold_trigger_id IS NOT NULL AND hold_started_at IS NOT NULL AND hold_due_at IS NOT NULL
            AND hold_due_at > hold_started_at)),
    CHECK (
        (kind = 'run' AND run_snapshot_json IS NOT NULL AND run_source IS NOT NULL
            AND run_status IS NOT NULL AND run_started_at IS NOT NULL AND run_matched_trigger_ids_json IS NOT NULL
            AND skip_matched_triggers_json IS NULL AND skip_reason IS NULL AND skip_source IS NULL
            AND (run_status = 'running') = (run_completed_at IS NULL)
            AND (run_status IN ('failed', 'interrupted')) = (run_failure_code IS NOT NULL)
            AND ((run_source = 'device_fact' AND fact_id IS NOT NULL AND hold_trigger_id IS NULL)
                OR (run_source = 'manual' AND fact_id IS NULL AND hold_trigger_id IS NULL)
                OR (run_source = 'held_state' AND fact_id IS NULL AND hold_trigger_id IS NOT NULL
                    AND json_array_length(run_matched_trigger_ids_json) = 1)
                OR (run_source = 'schedule' AND fact_id IS NULL AND hold_trigger_id IS NULL
                    AND json_array_length(run_matched_trigger_ids_json) BETWEEN 1 AND 32)))
        OR (kind = 'skip' AND skip_matched_triggers_json IS NOT NULL AND skip_reason IS NOT NULL AND skip_source IS NOT NULL
            AND ((skip_source = 'device_fact' AND fact_id IS NOT NULL AND hold_trigger_id IS NULL
                    AND json_array_length(skip_matched_triggers_json) > 0)
                OR (skip_source = 'manual' AND fact_id IS NULL AND hold_trigger_id IS NULL
                    AND json_array_length(skip_matched_triggers_json) = 0
                    AND skip_reason IN ('conditions_false', 'conditions_unknown'))
                OR (skip_source = 'held_state' AND fact_id IS NULL AND hold_trigger_id IS NOT NULL
                    AND json_array_length(skip_matched_triggers_json) = 1
                    AND skip_reason IN ('automation_busy', 'conditions_false', 'conditions_unknown'))
                OR (skip_source = 'schedule' AND fact_id IS NULL AND hold_trigger_id IS NULL
                    AND json_array_length(skip_matched_triggers_json) BETWEEN 1 AND 32
                    AND skip_reason IN ('automation_busy', 'conditions_false', 'conditions_unknown')))
            AND run_snapshot_json IS NULL AND run_source IS NULL AND run_status IS NULL AND run_failure_code IS NULL
            AND run_started_at IS NULL AND run_completed_at IS NULL AND run_matched_trigger_ids_json IS NULL)),
    CHECK (COALESCE(run_source, skip_source) <> 'schedule' OR (
        fact_previous_value_json IS NULL AND condition_bypassed = 0
        AND json_extract(condition_decision_json, '$.bypass_requested') IS 0
        AND json_extract(condition_decision_json, '$.mode') IS condition_mode
        AND condition_mode IN ('not_configured', 'not_evaluated', 'evaluated')
        AND ((kind = 'run' AND ((condition_mode = 'not_configured' AND condition_result IS NULL)
                OR (condition_mode = 'evaluated' AND condition_result IS NOT NULL AND condition_result = 'true')))
            OR (kind = 'skip' AND (
                (skip_reason = 'automation_busy' AND condition_mode IN ('not_configured', 'not_evaluated')
                    AND condition_result IS NULL)
                OR (skip_reason = 'conditions_false' AND condition_mode = 'evaluated'
                    AND condition_result IS NOT NULL AND condition_result = 'false')
                OR (skip_reason = 'conditions_unknown' AND condition_mode = 'evaluated'
                    AND condition_result IS NOT NULL AND condition_result = 'unknown'))))))
);
INSERT INTO automation_history_new SELECT * FROM automation_history;
CREATE TEMP TABLE command_kind_steps_backup AS SELECT * FROM automation_run_steps;
CREATE TEMP TABLE command_kind_branches_backup AS SELECT * FROM automation_run_branch_decisions;
CREATE TEMP TABLE command_kind_delays_backup AS SELECT * FROM automation_run_delays;
CREATE TEMP TABLE command_kind_notifications_backup AS SELECT * FROM automation_run_notifications;
DELETE FROM automation_run_steps;
DELETE FROM automation_run_branch_decisions;
DELETE FROM automation_run_delays;
DELETE FROM automation_run_notifications;
DROP TABLE automation_history;
ALTER TABLE automation_history_new RENAME TO automation_history;
INSERT INTO automation_run_steps SELECT * FROM command_kind_steps_backup;
INSERT INTO automation_run_branch_decisions SELECT * FROM command_kind_branches_backup;
INSERT INTO automation_run_delays SELECT * FROM command_kind_delays_backup;
INSERT INTO automation_run_notifications SELECT * FROM command_kind_notifications_backup;
DROP TABLE command_kind_steps_backup;
DROP TABLE command_kind_branches_backup;
DROP TABLE command_kind_delays_backup;
DROP TABLE command_kind_notifications_backup;
CREATE INDEX automation_history_page_idx ON automation_history(automation_id, recorded_at DESC, id DESC);
CREATE UNIQUE INDEX automation_history_one_running_run_idx ON automation_history(automation_id)
    WHERE kind = 'run' AND run_status = 'running';
CREATE UNIQUE INDEX automation_history_fact_outcome_idx ON automation_history(fact_id, automation_id)
    WHERE fact_id IS NOT NULL;

-- +goose StatementBegin
CREATE TRIGGER automation_schedule_history_insert BEFORE INSERT ON automation_history
WHEN COALESCE(NEW.run_source, NEW.skip_source) = 'schedule'
BEGIN
    SELECT RAISE(ABORT, 'schedule history requires Cron Trigger provenance') WHERE
        (NEW.kind = 'skip' AND EXISTS (
            SELECT 1 FROM json_each(NEW.skip_matched_triggers_json)
            WHERE type <> 'object' OR json_extract(value, '$.kind') IS NOT 'cron'))
        OR (NEW.kind = 'run' AND EXISTS (
            SELECT 1 FROM json_each(NEW.run_matched_trigger_ids_json) AS matched
            WHERE matched.type <> 'text' OR NOT EXISTS (
                SELECT 1 FROM json_each(NEW.run_snapshot_json, '$.triggers') AS trigger
                WHERE json_extract(trigger.value, '$.id') = matched.value
                    AND json_extract(trigger.value, '$.kind') = 'cron')));
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER automation_schedule_history_update BEFORE UPDATE ON automation_history
WHEN COALESCE(NEW.run_source, NEW.skip_source) = 'schedule'
BEGIN
    SELECT RAISE(ABORT, 'schedule history requires Cron Trigger provenance') WHERE
        (NEW.kind = 'skip' AND EXISTS (
            SELECT 1 FROM json_each(NEW.skip_matched_triggers_json)
            WHERE type <> 'object' OR json_extract(value, '$.kind') IS NOT 'cron'))
        OR (NEW.kind = 'run' AND EXISTS (
            SELECT 1 FROM json_each(NEW.run_matched_trigger_ids_json) AS matched
            WHERE matched.type <> 'text' OR NOT EXISTS (
                SELECT 1 FROM json_each(NEW.run_snapshot_json, '$.triggers') AS trigger
                WHERE json_extract(trigger.value, '$.id') = matched.value
                    AND json_extract(trigger.value, '$.kind') = 'cron')));
END;
-- +goose StatementEnd

-- Upgrade only executable Steps, not lookalike objects inside parameters.
CREATE TEMP TABLE automation_command_kind_upgrades AS
WITH RECURSIVE
documents(source, id, document) AS (
    SELECT 'definition', id, definition_json FROM automations
    UNION ALL
    SELECT 'snapshot', id, run_snapshot_json FROM automation_history
    WHERE run_snapshot_json IS NOT NULL
),
sequences(source, id, path, steps) AS (
    SELECT source, id, '$.steps', json_extract(document, '$.steps')
    FROM documents WHERE json_type(document, '$.steps') = 'array'
    UNION ALL
    SELECT sequences.source, sequences.id,
        sequences.path || '[' || step.key || '].' || child.key, child.value
    FROM sequences, json_each(sequences.steps) AS step, json_each(step.value) AS child
    WHERE json_extract(step.value, '$.kind') IN ('if', 'choose')
        AND child.key IN ('then', 'else', 'default') AND child.type = 'array'
    UNION ALL
    SELECT sequences.source, sequences.id,
        sequences.path || '[' || step.key || '].branches[' || branch.key || '].steps',
        json_extract(branch.value, '$.steps')
    FROM sequences, json_each(sequences.steps) AS step,
        json_each(step.value, '$.branches') AS branch
    WHERE json_extract(step.value, '$.kind') = 'choose'
        AND json_type(branch.value, '$.steps') = 'array'
),
changes(source, id, path, ordinal) AS (
    SELECT sequences.source, sequences.id, sequences.path || '[' || step.key || '].kind',
        row_number() OVER (PARTITION BY sequences.source, sequences.id ORDER BY sequences.path, step.key)
    FROM sequences, json_each(sequences.steps) AS step
    WHERE step.type = 'object' AND json_type(step.value, '$.kind') IS NULL
        AND json_type(step.value, '$.entity_id') = 'text'
        AND json_type(step.value, '$.operation') = 'text'
        AND json_type(step.value, '$.parameters') = 'object'
),
rewrites(source, id, ordinal, document) AS (
    SELECT source, id, 0, document FROM documents
    WHERE EXISTS (SELECT 1 FROM changes WHERE changes.source = documents.source AND changes.id = documents.id)
    UNION ALL
    SELECT rewrites.source, rewrites.id, changes.ordinal,
        json_insert(rewrites.document, changes.path, 'command')
    FROM rewrites JOIN changes ON changes.source = rewrites.source AND changes.id = rewrites.id
        AND changes.ordinal = rewrites.ordinal + 1
)
SELECT source, id, document FROM rewrites
WHERE NOT EXISTS (SELECT 1 FROM changes WHERE changes.source = rewrites.source AND changes.id = rewrites.id
    AND changes.ordinal = rewrites.ordinal + 1);
UPDATE automations SET definition_json = (
    SELECT document FROM automation_command_kind_upgrades WHERE source = 'definition' AND id = automations.id
)
WHERE id IN (SELECT id FROM automation_command_kind_upgrades WHERE source = 'definition');
UPDATE automation_history SET run_snapshot_json = (
    SELECT document FROM automation_command_kind_upgrades WHERE source = 'snapshot' AND id = automation_history.id
)
WHERE id IN (SELECT id FROM automation_command_kind_upgrades WHERE source = 'snapshot');
DROP TABLE automation_command_kind_upgrades;

-- +goose Down
-- Keep canonical data and its bounded allowance. Narrowing would reject valid
-- upgraded definitions; an older binary requires a matching pre-upgrade backup.
SELECT 1;
