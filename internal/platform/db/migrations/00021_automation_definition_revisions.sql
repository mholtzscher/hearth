-- +goose Up
-- Audit before discarding any inline configuration. JSON tree paths ignore
-- object member order; raw leaf tokens preserve exact numbers and JSON null.
CREATE TEMP TABLE automation_revision_sources (
    source_id TEXT NOT NULL,
    automation_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    definition_json TEXT NOT NULL,
    PRIMARY KEY (automation_id, revision)
);
-- +goose StatementBegin
CREATE TEMP TRIGGER automation_revision_source_audit BEFORE INSERT ON automation_revision_sources
BEGIN
    SELECT RAISE(ABORT, 'Automation revision migration: missing definition for ' || NEW.source_id)
    WHERE json_type(NEW.definition_json) IS NOT 'object';
    -- Path-based extraction cannot distinguish repeated members of one object.
    SELECT RAISE(ABORT, 'Automation revision migration: ambiguous duplicate JSON members in definition for ' ||
        NEW.source_id || '; inspect the original document and repair before retrying')
    WHERE EXISTS (SELECT 1 FROM json_tree(NEW.definition_json)
        WHERE typeof(key) = 'text' GROUP BY parent, key HAVING count(*) > 1);
    SELECT RAISE(ABORT, 'Automation revision migration: conflicting definitions for ' ||
        NEW.automation_id || ' revision ' || NEW.revision || ' at ' || NEW.source_id ||
        '; inspect original snapshots and restore or repair before retrying')
    WHERE EXISTS (
        SELECT 1 FROM automation_revision_sources AS previous
        WHERE previous.automation_id = NEW.automation_id AND previous.revision = NEW.revision
          AND (EXISTS (
                SELECT fullkey, type, CASE WHEN atom IS NOT NULL OR type = 'null'
                    THEN NEW.definition_json -> fullkey END FROM json_tree(NEW.definition_json)
                EXCEPT
                SELECT fullkey, type, CASE WHEN atom IS NOT NULL OR type = 'null'
                    THEN previous.definition_json -> fullkey END FROM json_tree(previous.definition_json))
            OR EXISTS (
                SELECT fullkey, type, CASE WHEN atom IS NOT NULL OR type = 'null'
                    THEN previous.definition_json -> fullkey END FROM json_tree(previous.definition_json)
                EXCEPT
                SELECT fullkey, type, CASE WHEN atom IS NOT NULL OR type = 'null'
                    THEN NEW.definition_json -> fullkey END FROM json_tree(NEW.definition_json)))
    );
END;
-- +goose StatementEnd
INSERT OR IGNORE INTO automation_revision_sources
    SELECT id, id, revision, definition_json FROM automations;
INSERT OR IGNORE INTO automation_revision_sources
    SELECT id, automation_id, revision, run_snapshot_json FROM automation_history WHERE kind = 'run';

CREATE TEMP TABLE automation_run_condition_audit (
    run_id TEXT NOT NULL,
    definition_json TEXT NOT NULL,
    decision_json TEXT NOT NULL
);
-- +goose StatementBegin
CREATE TEMP TRIGGER automation_run_condition_audit_insert BEFORE INSERT ON automation_run_condition_audit
BEGIN
    SELECT RAISE(ABORT, 'Automation revision migration: ambiguous duplicate JSON members in Condition decision for Run ' ||
        NEW.run_id || '; inspect the original evidence and repair before retrying')
    WHERE EXISTS (SELECT 1 FROM json_tree(NEW.decision_json)
        WHERE typeof(key) = 'text' GROUP BY parent, key HAVING count(*) > 1);
    SELECT RAISE(ABORT, 'Automation revision migration: missing or mismatched Condition configuration in Run ' ||
        NEW.run_id || '; inspect original evidence and restore or repair before retrying')
    WHERE json_type(NEW.decision_json, '$.mode') IS NOT 'text'
       OR json_extract(NEW.decision_json, '$.mode') NOT IN ('not_configured', 'not_evaluated', 'bypassed', 'evaluated')
       OR (json_extract(NEW.decision_json, '$.mode') = 'not_configured'
           AND (json_type(NEW.definition_json, '$.conditions') IS NOT NULL
                OR json_type(NEW.decision_json, '$.snapshot') IS NOT NULL))
       OR (json_extract(NEW.decision_json, '$.mode') <> 'not_configured'
           AND (json_type(NEW.definition_json, '$.conditions') IS NOT 'object'
                OR json_type(NEW.decision_json, '$.snapshot') IS NOT 'object'
                OR EXISTS (
                    SELECT fullkey, type, CASE WHEN atom IS NOT NULL OR type = 'null'
                        THEN json_extract(NEW.definition_json, '$.conditions') -> fullkey END
                    FROM json_tree(json_extract(NEW.definition_json, '$.conditions'))
                    EXCEPT
                    SELECT fullkey, type,
                        CASE WHEN atom IS NOT NULL OR type = 'null'
                            THEN json_extract(NEW.decision_json, '$.snapshot') -> fullkey END
                    FROM json_tree(json_extract(NEW.decision_json, '$.snapshot')))
                OR EXISTS (
                    SELECT fullkey, type, CASE WHEN atom IS NOT NULL OR type = 'null'
                        THEN json_extract(NEW.decision_json, '$.snapshot') -> fullkey END
                    FROM json_tree(json_extract(NEW.decision_json, '$.snapshot'))
                    EXCEPT
                    SELECT fullkey, type, CASE WHEN atom IS NOT NULL OR type = 'null'
                        THEN json_extract(NEW.definition_json, '$.conditions') -> fullkey END
                    FROM json_tree(json_extract(NEW.definition_json, '$.conditions')))));
END;
-- +goose StatementEnd
INSERT INTO automation_run_condition_audit
    SELECT id, run_snapshot_json, condition_decision_json FROM automation_history WHERE kind = 'run';
DROP TABLE automation_run_condition_audit;

CREATE TABLE automation_definition_revisions (
    automation_id TEXT NOT NULL CHECK (length(automation_id) = 40 AND substr(automation_id, 1, 4) = 'aut_'),
    revision INTEGER NOT NULL CHECK (revision >= 1),
    definition_json TEXT NOT NULL CHECK (json_valid(definition_json)
        AND json_type(definition_json) = 'object'
        AND length(CAST(definition_json AS BLOB)) <= 66080),
    PRIMARY KEY (automation_id, revision)
);
INSERT INTO automation_definition_revisions
    SELECT automation_id, revision, definition_json FROM automation_revision_sources;
DROP TABLE automation_revision_sources;

CREATE TABLE automations_new (
    id TEXT PRIMARY KEY CHECK (length(id) = 40 AND substr(id, 1, 4) = 'aut_'),
    revision INTEGER NOT NULL CHECK (revision >= 1),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (id, revision) REFERENCES automation_definition_revisions(automation_id, revision)
);
INSERT INTO automations_new SELECT id, revision, created_at, updated_at FROM automations;
CREATE TEMP TABLE automation_revision_holds_backup AS SELECT * FROM automation_holds;
DELETE FROM automation_holds;
DROP TABLE automations;
ALTER TABLE automations_new RENAME TO automations;
INSERT INTO automation_holds SELECT * FROM automation_revision_holds_backup;
DROP TABLE automation_revision_holds_backup;

CREATE VIEW automation_definitions AS
    SELECT head.id, head.revision, artifact.definition_json, head.created_at, head.updated_at
    FROM automations AS head
    JOIN automation_definition_revisions AS artifact
      ON artifact.automation_id = head.id AND artifact.revision = head.revision;

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
    definition_revision INTEGER,
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
    FOREIGN KEY (automation_id, definition_revision)
        REFERENCES automation_definition_revisions(automation_id, revision),
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
        (kind = 'run' AND definition_revision IS NOT NULL AND definition_revision = revision
            AND run_source IS NOT NULL AND run_status IS NOT NULL AND run_started_at IS NOT NULL
            AND run_matched_trigger_ids_json IS NOT NULL
            AND skip_matched_triggers_json IS NULL AND skip_reason IS NULL AND skip_source IS NULL
            AND json_type(condition_decision_json, '$.snapshot') IS NULL
            AND (run_status = 'running') = (run_completed_at IS NULL)
            AND (run_status IN ('failed', 'interrupted')) = (run_failure_code IS NOT NULL)
            AND ((run_source = 'device_fact' AND fact_id IS NOT NULL AND hold_trigger_id IS NULL)
                OR (run_source = 'manual' AND fact_id IS NULL AND hold_trigger_id IS NULL)
                OR (run_source = 'held_state' AND fact_id IS NULL AND hold_trigger_id IS NOT NULL
                    AND json_array_length(run_matched_trigger_ids_json) = 1)
                OR (run_source = 'schedule' AND fact_id IS NULL AND hold_trigger_id IS NULL
                    AND json_array_length(run_matched_trigger_ids_json) BETWEEN 1 AND 32)))
        OR (kind = 'skip' AND definition_revision IS NULL AND skip_matched_triggers_json IS NOT NULL
            AND skip_reason IS NOT NULL AND skip_source IS NOT NULL
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
            AND run_source IS NULL AND run_status IS NULL AND run_failure_code IS NULL
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
INSERT INTO automation_history_new
SELECT id, automation_id, automation_name, kind, revision, recorded_at,
    fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id, fact_value_json,
    fact_emitted_at, fact_previous_value_json, CASE WHEN kind = 'run' THEN revision END,
    run_source, run_status, run_failure_code, run_started_at, run_completed_at, run_matched_trigger_ids_json,
    skip_matched_triggers_json, skip_reason, skip_source, hold_trigger_id, hold_started_at, hold_due_at,
    CASE WHEN kind = 'run' THEN json_remove(condition_decision_json, '$.snapshot') ELSE condition_decision_json END,
    condition_mode, condition_bypassed, condition_result
FROM automation_history;

CREATE TEMP TABLE automation_revision_steps_backup AS SELECT * FROM automation_run_steps;
CREATE TEMP TABLE automation_revision_branches_backup AS SELECT * FROM automation_run_branch_decisions;
CREATE TEMP TABLE automation_revision_delays_backup AS SELECT * FROM automation_run_delays;
CREATE TEMP TABLE automation_revision_notifications_backup AS SELECT * FROM automation_run_notifications;
CREATE TEMP TABLE automation_revision_functions_backup AS SELECT * FROM automation_run_functions;
DELETE FROM automation_run_steps;
DELETE FROM automation_run_branch_decisions;
DELETE FROM automation_run_delays;
DELETE FROM automation_run_notifications;
DELETE FROM automation_run_functions;
DROP TABLE automation_history;
ALTER TABLE automation_history_new RENAME TO automation_history;
INSERT INTO automation_run_steps SELECT * FROM automation_revision_steps_backup;
INSERT INTO automation_run_branch_decisions SELECT * FROM automation_revision_branches_backup;
INSERT INTO automation_run_delays SELECT * FROM automation_revision_delays_backup;
INSERT INTO automation_run_notifications SELECT * FROM automation_revision_notifications_backup;
INSERT INTO automation_run_functions SELECT * FROM automation_revision_functions_backup;
DROP TABLE automation_revision_steps_backup;
DROP TABLE automation_revision_branches_backup;
DROP TABLE automation_revision_delays_backup;
DROP TABLE automation_revision_notifications_backup;
DROP TABLE automation_revision_functions_backup;

CREATE INDEX automation_history_page_idx ON automation_history(automation_id, recorded_at DESC, id DESC);
CREATE UNIQUE INDEX automation_history_one_running_run_idx ON automation_history(automation_id)
    WHERE kind = 'run' AND run_status = 'running';
CREATE UNIQUE INDEX automation_history_fact_outcome_idx ON automation_history(fact_id, automation_id)
    WHERE fact_id IS NOT NULL;
CREATE INDEX automation_history_retention_idx ON automation_history(recorded_at, id)
    WHERE kind = 'skip' OR run_status <> 'running';
CREATE INDEX automation_history_definition_revision_idx ON automation_history(automation_id, definition_revision)
    WHERE definition_revision IS NOT NULL;

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
                SELECT 1 FROM automation_definition_revisions AS artifact,
                    json_each(artifact.definition_json, '$.triggers') AS trigger
                WHERE artifact.automation_id = NEW.automation_id AND artifact.revision = NEW.definition_revision
                    AND json_extract(trigger.value, '$.id') = matched.value
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
                SELECT 1 FROM automation_definition_revisions AS artifact,
                    json_each(artifact.definition_json, '$.triggers') AS trigger
                WHERE artifact.automation_id = NEW.automation_id AND artifact.revision = NEW.definition_revision
                    AND json_extract(trigger.value, '$.id') = matched.value
                    AND json_extract(trigger.value, '$.kind') = 'cron')));
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER automation_definition_revision_immutable BEFORE UPDATE ON automation_definition_revisions
BEGIN
    SELECT RAISE(ABORT, 'Automation definition revisions are immutable');
END;
-- +goose StatementEnd

-- +goose Down
CREATE TEMP TABLE automation_revision_rollback_guard (id INTEGER);
-- +goose StatementBegin
CREATE TEMP TRIGGER automation_revision_rollback_abort BEFORE INSERT ON automation_revision_rollback_guard
BEGIN
    SELECT RAISE(ABORT, 'Restore the pre-upgrade backup with its matching binary; revision sharing has no Down migration');
END;
-- +goose StatementEnd
INSERT INTO automation_revision_rollback_guard VALUES (1);
