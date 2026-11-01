-- +goose Up
-- Preserve dependent rows with foreign keys enabled and without rewriting JSON.
CREATE TEMP TABLE reusable_saved_history AS SELECT * FROM automation_history;
CREATE TEMP TABLE reusable_saved_steps AS SELECT * FROM automation_step_executions;
DELETE FROM automation_history;
DROP TABLE automation_step_executions;
DROP TABLE automation_history;

CREATE TABLE automation_history (
    id TEXT PRIMARY KEY CHECK ((length(id) = 40 AND substr(id, 1, 4) = 'arn_') OR (length(id) = 40 AND substr(id, 1, 4) = 'ask_')),
    automation_id TEXT NOT NULL CHECK (length(automation_id) = 40 AND substr(automation_id, 1, 4) = 'aut_'),
    automation_name TEXT NOT NULL CHECK (length(automation_name) BETWEEN 1 AND 200),
    kind TEXT NOT NULL CHECK (kind IN ('run', 'skip')),
    revision INTEGER NOT NULL CHECK (revision >= 1), recorded_at TEXT NOT NULL,
    fact_id TEXT CHECK (fact_id IS NULL OR (length(fact_id) = 40 AND substr(fact_id, 1, 4) = 'fct_')),
    fact_family TEXT CHECK (fact_family IS NULL OR fact_family IN ('observation', 'entity_event')),
    fact_entity_id TEXT CHECK (fact_entity_id IS NULL OR (length(fact_entity_id) = 40 AND substr(fact_entity_id, 1, 4) = 'ent_')),
    fact_variant TEXT, fact_causation_id TEXT,
    fact_value_json TEXT CHECK (fact_value_json IS NULL OR json_valid(fact_value_json)), fact_emitted_at TEXT,
    fact_previous_value_json TEXT CHECK (fact_previous_value_json IS NULL OR json_valid(fact_previous_value_json)),
    definition_revision INTEGER,
    run_source TEXT CHECK (run_source IS NULL OR run_source IN ('device_fact', 'manual', 'held_state', 'schedule', 'invocation')),
    run_status TEXT CHECK (run_status IS NULL OR run_status IN ('running', 'succeeded', 'failed', 'interrupted')),
    run_failure_code TEXT, run_started_at TEXT, run_completed_at TEXT,
    run_matched_trigger_ids_json TEXT CHECK (run_matched_trigger_ids_json IS NULL OR (json_valid(run_matched_trigger_ids_json) AND json_type(run_matched_trigger_ids_json) = 'array')),
    skip_matched_triggers_json TEXT CHECK (skip_matched_triggers_json IS NULL OR (json_valid(skip_matched_triggers_json) AND json_type(skip_matched_triggers_json) = 'array')),
    skip_reason TEXT CHECK (skip_reason IS NULL OR skip_reason IN ('automation_busy', 'stale_fact', 'conditions_false', 'conditions_unknown')),
    skip_source TEXT CHECK (skip_source IS NULL OR skip_source IN ('device_fact', 'manual', 'held_state', 'schedule', 'invocation')),
    hold_trigger_id TEXT CHECK (hold_trigger_id IS NULL OR (length(hold_trigger_id) BETWEEN 1 AND 63 AND substr(hold_trigger_id, 1, 1) GLOB '[a-z0-9]' AND hold_trigger_id NOT GLOB '*[^a-z0-9_-]*')),
    hold_started_at TEXT, hold_due_at TEXT,
    condition_decision_json TEXT NOT NULL CHECK (json_valid(condition_decision_json) AND json_type(condition_decision_json) = 'object'),
    condition_mode TEXT NOT NULL DEFAULT 'not_configured' CHECK (condition_mode IN ('not_configured', 'not_evaluated', 'bypassed', 'evaluated')),
    condition_bypassed INTEGER NOT NULL DEFAULT 0 CHECK (condition_bypassed IN (0, 1)),
    condition_result TEXT CHECK (condition_result IS NULL OR condition_result IN ('true', 'false', 'unknown')),
    data_evidence TEXT NOT NULL DEFAULT 'legacy_unavailable' CHECK (data_evidence IN ('recorded', 'legacy_unavailable')),
    value_initialization_json TEXT CHECK (value_initialization_json IS NULL OR (json_valid(value_initialization_json) AND json_type(value_initialization_json) = 'object' AND length(CAST(value_initialization_json AS BLOB)) <= 131072)),
    parent_automation_id TEXT, parent_run_id TEXT, parent_step_id TEXT,
    FOREIGN KEY (automation_id, definition_revision) REFERENCES automation_definition_revisions(automation_id, revision),
    CHECK ((fact_id IS NULL AND fact_family IS NULL AND fact_entity_id IS NULL AND fact_variant IS NULL AND fact_causation_id IS NULL AND fact_value_json IS NULL AND fact_emitted_at IS NULL)
        OR (fact_id IS NOT NULL AND fact_family IS NOT NULL AND fact_entity_id IS NOT NULL AND fact_variant IS NOT NULL AND fact_causation_id IS NOT NULL AND fact_emitted_at IS NOT NULL
            AND (fact_family <> 'observation' OR fact_value_json IS NOT NULL) AND (fact_family <> 'entity_event' OR fact_value_json IS NULL))),
    CHECK ((hold_trigger_id IS NULL AND hold_started_at IS NULL AND hold_due_at IS NULL)
        OR (hold_trigger_id IS NOT NULL AND hold_started_at IS NOT NULL AND hold_due_at IS NOT NULL AND hold_due_at > hold_started_at)),
    CHECK ((kind = 'run' AND definition_revision IS NOT NULL AND definition_revision = revision
            AND run_source IS NOT NULL AND run_status IS NOT NULL AND run_started_at IS NOT NULL AND run_matched_trigger_ids_json IS NOT NULL
            AND skip_matched_triggers_json IS NULL AND skip_reason IS NULL AND skip_source IS NULL
            AND json_type(condition_decision_json, '$.snapshot') IS NULL
            AND (run_status = 'running') = (run_completed_at IS NULL)
            AND (run_status IN ('failed', 'interrupted')) = (run_failure_code IS NOT NULL)
            AND ((run_source = 'device_fact' AND fact_id IS NOT NULL AND hold_trigger_id IS NULL)
                OR (run_source = 'manual' AND fact_id IS NULL AND hold_trigger_id IS NULL)
                OR (run_source = 'invocation' AND fact_id IS NULL AND hold_trigger_id IS NULL AND json_array_length(run_matched_trigger_ids_json) = 0)
                OR (run_source = 'held_state' AND fact_id IS NULL AND hold_trigger_id IS NOT NULL AND json_array_length(run_matched_trigger_ids_json) = 1)
                OR (run_source = 'schedule' AND fact_id IS NULL AND hold_trigger_id IS NULL AND json_array_length(run_matched_trigger_ids_json) BETWEEN 1 AND 32)))
        OR (kind = 'skip' AND definition_revision IS NULL AND skip_matched_triggers_json IS NOT NULL AND skip_reason IS NOT NULL AND skip_source IS NOT NULL
            AND ((skip_source = 'device_fact' AND fact_id IS NOT NULL AND hold_trigger_id IS NULL AND json_array_length(skip_matched_triggers_json) > 0)
                OR (skip_source IN ('manual', 'invocation') AND fact_id IS NULL AND hold_trigger_id IS NULL AND json_array_length(skip_matched_triggers_json) = 0 AND skip_reason IN ('conditions_false', 'conditions_unknown'))
                OR (skip_source = 'held_state' AND fact_id IS NULL AND hold_trigger_id IS NOT NULL AND json_array_length(skip_matched_triggers_json) = 1 AND skip_reason IN ('automation_busy', 'conditions_false', 'conditions_unknown'))
                OR (skip_source = 'schedule' AND fact_id IS NULL AND hold_trigger_id IS NULL AND json_array_length(skip_matched_triggers_json) BETWEEN 1 AND 32 AND skip_reason IN ('automation_busy', 'conditions_false', 'conditions_unknown')))
            AND run_source IS NULL AND run_status IS NULL AND run_failure_code IS NULL AND run_started_at IS NULL AND run_completed_at IS NULL AND run_matched_trigger_ids_json IS NULL)),
    CHECK (COALESCE(run_source, skip_source) <> 'invocation' OR (condition_bypassed = 0 AND fact_previous_value_json IS NULL
        AND json_extract(condition_decision_json, '$.bypass_requested') IS 0)),
    CHECK ((COALESCE(run_source, skip_source) = 'invocation' AND parent_automation_id IS NOT NULL AND length(parent_automation_id) = 40 AND substr(parent_automation_id, 1, 4) = 'aut_'
        AND parent_run_id IS NOT NULL AND length(parent_run_id) = 40 AND substr(parent_run_id, 1, 4) = 'arn_'
        AND parent_step_id IS NOT NULL AND length(parent_step_id) BETWEEN 1 AND 63 AND substr(parent_step_id, 1, 1) GLOB '[a-z0-9]' AND parent_step_id NOT GLOB '*[^a-z0-9_-]*')
        OR (COALESCE(run_source, skip_source) <> 'invocation' AND parent_automation_id IS NULL AND parent_run_id IS NULL AND parent_step_id IS NULL)),
    CHECK (COALESCE(run_source, skip_source) <> 'invocation' OR (
        substr(parent_automation_id, 5) NOT GLOB '*[^0-9a-f-]*' AND substr(parent_run_id, 5) NOT GLOB '*[^0-9a-f-]*'
        AND length(replace(substr(parent_automation_id, 5), '-', '')) = 32 AND length(replace(substr(parent_run_id, 5), '-', '')) = 32
        AND substr(parent_automation_id, 13, 1) = '-' AND substr(parent_automation_id, 18, 2) = '-7' AND substr(parent_automation_id, 23, 1) = '-' AND substr(parent_automation_id, 24, 1) GLOB '[89ab]' AND substr(parent_automation_id, 28, 1) = '-'
        AND substr(parent_run_id, 13, 1) = '-' AND substr(parent_run_id, 18, 2) = '-7' AND substr(parent_run_id, 23, 1) = '-' AND substr(parent_run_id, 24, 1) GLOB '[89ab]' AND substr(parent_run_id, 28, 1) = '-')),
    CHECK (COALESCE(run_source, skip_source) <> 'schedule' OR (
        fact_previous_value_json IS NULL AND condition_bypassed = 0 AND json_extract(condition_decision_json, '$.bypass_requested') IS 0
        AND json_extract(condition_decision_json, '$.mode') IS condition_mode AND condition_mode IN ('not_configured', 'not_evaluated', 'evaluated')
        AND ((kind = 'run' AND ((condition_mode = 'not_configured' AND condition_result IS NULL) OR (condition_mode = 'evaluated' AND condition_result IS NOT NULL AND condition_result = 'true')))
            OR (kind = 'skip' AND ((skip_reason = 'automation_busy' AND condition_mode IN ('not_configured', 'not_evaluated') AND condition_result IS NULL)
                OR (skip_reason = 'conditions_false' AND condition_mode = 'evaluated' AND condition_result IS NOT NULL AND condition_result = 'false')
                OR (skip_reason = 'conditions_unknown' AND condition_mode = 'evaluated' AND condition_result IS NOT NULL AND condition_result = 'unknown'))))))
);
INSERT INTO automation_history SELECT *, NULL, NULL, NULL FROM reusable_saved_history;
CREATE INDEX automation_history_page_idx ON automation_history(automation_id, recorded_at DESC, id DESC);
CREATE UNIQUE INDEX automation_history_one_running_run_idx ON automation_history(automation_id) WHERE kind = 'run' AND run_status = 'running';
CREATE UNIQUE INDEX automation_history_fact_outcome_idx ON automation_history(fact_id, automation_id) WHERE fact_id IS NOT NULL;
CREATE INDEX automation_history_retention_idx ON automation_history(recorded_at, id) WHERE kind = 'skip' OR run_status <> 'running';
CREATE INDEX automation_history_definition_revision_idx ON automation_history(automation_id, definition_revision) WHERE definition_revision IS NOT NULL;
-- +goose StatementBegin
CREATE TRIGGER automation_schedule_history_insert BEFORE INSERT ON automation_history WHEN COALESCE(NEW.run_source, NEW.skip_source) = 'schedule'
BEGIN
    SELECT RAISE(ABORT, 'schedule history requires Cron Trigger provenance') WHERE
        (NEW.kind = 'skip' AND EXISTS (SELECT 1 FROM json_each(NEW.skip_matched_triggers_json) WHERE type <> 'object' OR json_extract(value, '$.kind') IS NOT 'cron'))
        OR (NEW.kind = 'run' AND EXISTS (SELECT 1 FROM json_each(NEW.run_matched_trigger_ids_json) AS matched WHERE matched.type <> 'text' OR NOT EXISTS
            (SELECT 1 FROM automation_definition_revisions AS artifact, json_each(artifact.definition_json, '$.triggers') AS trigger
             WHERE artifact.automation_id = NEW.automation_id AND artifact.revision = NEW.definition_revision AND json_extract(trigger.value, '$.id') = matched.value AND json_extract(trigger.value, '$.kind') = 'cron')));
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER automation_schedule_history_update BEFORE UPDATE ON automation_history WHEN COALESCE(NEW.run_source, NEW.skip_source) = 'schedule'
BEGIN
    SELECT RAISE(ABORT, 'schedule history requires Cron Trigger provenance') WHERE
        (NEW.kind = 'skip' AND EXISTS (SELECT 1 FROM json_each(NEW.skip_matched_triggers_json) WHERE type <> 'object' OR json_extract(value, '$.kind') IS NOT 'cron'))
        OR (NEW.kind = 'run' AND EXISTS (SELECT 1 FROM json_each(NEW.run_matched_trigger_ids_json) AS matched WHERE matched.type <> 'text' OR NOT EXISTS
            (SELECT 1 FROM automation_definition_revisions AS artifact, json_each(artifact.definition_json, '$.triggers') AS trigger
             WHERE artifact.automation_id = NEW.automation_id AND artifact.revision = NEW.definition_revision AND json_extract(trigger.value, '$.id') = matched.value AND json_extract(trigger.value, '$.kind') = 'cron')));
END;
-- +goose StatementEnd

CREATE TABLE automation_step_executions (
    run_id TEXT NOT NULL REFERENCES automation_history(id) ON DELETE CASCADE CHECK (length(run_id) = 40 AND substr(run_id, 1, 4) = 'arn_'),
    step_id TEXT NOT NULL CHECK (length(step_id) BETWEEN 1 AND 63 AND substr(step_id, 1, 1) GLOB '[a-z0-9]' AND step_id NOT GLOB '*[^a-z0-9_-]*'),
    sequence INTEGER NOT NULL CHECK (sequence BETWEEN 0 AND 63),
    kind TEXT NOT NULL CHECK (kind IN ('command', 'delay', 'notification', 'function', 'if', 'choose', 'read_state', 'set_variables', 'sub_automation')),
    phase TEXT NOT NULL CHECK (phase IN ('running', 'succeeded', 'failed', 'interrupted')),
    started_at TEXT NOT NULL, completed_at TEXT,
    evidence_json TEXT NOT NULL CHECK (json_valid(evidence_json) AND json_type(evidence_json) = 'object'),
    PRIMARY KEY (run_id, step_id), UNIQUE (run_id, sequence),
    CHECK ((phase = 'running') = (completed_at IS NULL)),
    CHECK ((kind IN ('command', 'notification', 'function', 'sub_automation') AND phase IN ('running', 'succeeded', 'failed', 'interrupted'))
        OR (kind = 'delay' AND phase IN ('running', 'succeeded', 'interrupted'))
        OR (kind IN ('if', 'choose', 'read_state', 'set_variables') AND phase IN ('succeeded', 'failed') AND completed_at = started_at)),
    CHECK (json_type(evidence_json, '$.value_failure') IS NULL OR (phase = 'failed' AND json_type(evidence_json, '$.value_failure') IS 'object' AND json_type(evidence_json, '$.value_failure.failure_code') IS 'text')),
    CHECK (kind <> 'notification' OR (phase = 'failed' AND json_type(evidence_json, '$.value_failure') IS 'object' AND json_type(evidence_json, '$.attempt_id') IS NULL)
        OR (json_type(evidence_json, '$.attempt_id') IS 'text' AND length(json_extract(evidence_json, '$.attempt_id')) = 40 AND substr(json_extract(evidence_json, '$.attempt_id'), 1, 4) = 'ntf_')),
    CHECK (kind <> 'function' OR (phase = 'failed' AND json_type(evidence_json, '$.value_failure') IS 'object' AND json_type(evidence_json, '$.invocation_id') IS NULL)
        OR (json_type(evidence_json, '$.invocation_id') IS 'text' AND length(json_extract(evidence_json, '$.invocation_id')) = 40 AND substr(json_extract(evidence_json, '$.invocation_id'), 1, 4) = 'afi_')),
    CHECK (kind <> 'command' OR ((phase = 'interrupted' AND json_type(evidence_json, '$.command_id') IS NULL AND json_type(evidence_json, '$.correlation_id') IS NULL AND json_type(evidence_json, '$.verified_command_id') IS NULL)
        OR (phase = 'failed' AND json_type(evidence_json, '$.value_failure') IS 'object' AND json_type(evidence_json, '$.command_id') IS NULL AND json_type(evidence_json, '$.correlation_id') IS NULL AND json_type(evidence_json, '$.verified_command_id') IS NULL)
        OR (json_type(evidence_json, '$.command_id') IS 'text' AND length(json_extract(evidence_json, '$.command_id')) = 40 AND substr(json_extract(evidence_json, '$.command_id'), 1, 4) = 'cmd_'
            AND json_type(evidence_json, '$.correlation_id') IS 'text' AND length(json_extract(evidence_json, '$.correlation_id')) = 40 AND substr(json_extract(evidence_json, '$.correlation_id'), 1, 4) = 'cor_'))),
    CHECK (kind <> 'command' OR phase <> 'succeeded' OR (json_type(evidence_json, '$.verified_command_id') IS 'text' AND json_extract(evidence_json, '$.verified_command_id') = json_extract(evidence_json, '$.command_id') AND COALESCE(json_extract(evidence_json, '$.command_outcome') IN ('satisfied', 'dispatched'), 0))),
    CHECK (phase NOT IN ('failed', 'interrupted') OR json_type(evidence_json, '$.value_failure') IS 'object' OR (kind IN ('if', 'choose') AND json_type(evidence_json, '$.failure_code') IS 'text')
        OR (kind NOT IN ('if', 'choose') AND json_type(evidence_json, '$.reason_code') IS 'text' AND length(json_extract(evidence_json, '$.reason_code')) > 0)),
    CHECK (kind <> 'notification' OR phase <> 'succeeded' OR json_extract(evidence_json, '$.outcome') IS 'accepted'),
    CHECK (kind <> 'function' OR phase <> 'succeeded' OR (json_type(evidence_json, '$.catalog_revision') IS 'text' AND json_extract(evidence_json, '$.invocation_outcome') IS 'succeeded')),
    CHECK (kind NOT IN ('if', 'choose') OR ((phase = 'succeeded' AND COALESCE(json_extract(evidence_json, '$.outcome') IN ('then', 'else', 'branch', 'default', 'no_match'), 0))
        OR (phase = 'failed' AND COALESCE(json_extract(evidence_json, '$.outcome') IN ('unknown', 'error'), 0)))),
    CHECK (kind <> 'sub_automation' OR COALESCE(((phase IN ('running', 'succeeded', 'interrupted') OR (phase = 'failed' AND json_type(evidence_json, '$.child_run_id') IS 'text'))
            AND json_type(evidence_json, '$.child_run_id') IS 'text' AND length(json_extract(evidence_json, '$.child_run_id')) = 40 AND substr(json_extract(evidence_json, '$.child_run_id'), 1, 4) = 'arn_'
            AND json_type(evidence_json, '$.skip_id') IS NULL
            AND ((phase IN ('running', 'succeeded') AND json_type(evidence_json, '$.reason_code') IS NULL AND json_type(evidence_json, '$.child_status') IS NULL AND json_type(evidence_json, '$.child_failure_code') IS NULL)
                OR (phase = 'interrupted' AND json_extract(evidence_json, '$.reason_code') IN ('core_stopping', 'core_restarted', 'executor_fault') AND json_type(evidence_json, '$.child_status') IS NULL AND json_type(evidence_json, '$.child_failure_code') IS NULL)
                OR (phase = 'failed' AND ((json_extract(evidence_json, '$.reason_code') = 'automation_run_failed' AND json_extract(evidence_json, '$.child_status') = 'failed') OR (json_extract(evidence_json, '$.reason_code') = 'automation_run_interrupted' AND json_extract(evidence_json, '$.child_status') = 'interrupted')) AND json_type(evidence_json, '$.child_failure_code') IS 'text' AND length(json_extract(evidence_json, '$.child_failure_code')) > 0)))
        OR (phase = 'failed' AND json_type(evidence_json, '$.child_run_id') IS NULL AND json_type(evidence_json, '$.child_status') IS NULL AND json_type(evidence_json, '$.child_failure_code') IS NULL
            AND ((json_extract(evidence_json, '$.reason_code') IN ('automation_not_found', 'automation_busy') AND json_type(evidence_json, '$.skip_id') IS NULL)
                OR (json_extract(evidence_json, '$.reason_code') IN ('conditions_false', 'conditions_unknown') AND json_type(evidence_json, '$.skip_id') IS 'text' AND length(json_extract(evidence_json, '$.skip_id')) = 40 AND substr(json_extract(evidence_json, '$.skip_id'), 1, 4) = 'ask_'))), 0))
    ,CHECK (kind <> 'sub_automation' OR json_type(evidence_json, '$.skip_id') IS NULL OR (
        substr(json_extract(evidence_json, '$.skip_id'), 5) NOT GLOB '*[^0-9a-f-]*'
        AND length(replace(substr(json_extract(evidence_json, '$.skip_id'), 5), '-', '')) = 32
        AND substr(json_extract(evidence_json, '$.skip_id'), 13, 1) = '-'
        AND substr(json_extract(evidence_json, '$.skip_id'), 18, 2) = '-7'
        AND substr(json_extract(evidence_json, '$.skip_id'), 23, 1) = '-'
        AND substr(json_extract(evidence_json, '$.skip_id'), 24, 1) GLOB '[89ab]'
        AND substr(json_extract(evidence_json, '$.skip_id'), 28, 1) = '-'))
    ,CHECK (kind <> 'sub_automation' OR json_type(evidence_json, '$.child_run_id') IS NULL OR (
        substr(json_extract(evidence_json, '$.child_run_id'), 5) NOT GLOB '*[^0-9a-f-]*'
        AND length(replace(substr(json_extract(evidence_json, '$.child_run_id'), 5), '-', '')) = 32
        AND substr(json_extract(evidence_json, '$.child_run_id'), 13, 1) = '-'
        AND substr(json_extract(evidence_json, '$.child_run_id'), 18, 2) = '-7'
        AND substr(json_extract(evidence_json, '$.child_run_id'), 23, 1) = '-'
        AND substr(json_extract(evidence_json, '$.child_run_id'), 24, 1) GLOB '[89ab]'
        AND substr(json_extract(evidence_json, '$.child_run_id'), 28, 1) = '-'))
);
INSERT INTO automation_step_executions SELECT * FROM reusable_saved_steps;
CREATE UNIQUE INDEX automation_step_executions_one_running_idx ON automation_step_executions(run_id) WHERE phase = 'running';
CREATE UNIQUE INDEX automation_step_executions_notification_attempt_idx ON automation_step_executions(json_extract(evidence_json, '$.attempt_id')) WHERE kind = 'notification';
CREATE UNIQUE INDEX automation_step_executions_function_invocation_idx ON automation_step_executions(json_extract(evidence_json, '$.invocation_id')) WHERE kind = 'function';
CREATE UNIQUE INDEX automation_step_executions_child_run_idx ON automation_step_executions(json_extract(evidence_json, '$.child_run_id')) WHERE kind = 'sub_automation' AND json_type(evidence_json, '$.child_run_id') IS 'text';
DROP TABLE reusable_saved_history;
DROP TABLE reusable_saved_steps;

-- +goose Down
CREATE TEMP TABLE reusable_rollback_guard (id INTEGER);
-- +goose StatementBegin
CREATE TEMP TRIGGER reusable_rollback_abort BEFORE INSERT ON reusable_rollback_guard
BEGIN
    SELECT RAISE(ABORT, 'Restore the pre-upgrade backup with its matching binary; reusable Automations have no lossless Down migration');
END;
-- +goose StatementEnd
INSERT INTO reusable_rollback_guard VALUES (1);
