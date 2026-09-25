-- +goose NO TRANSACTION
-- +goose Up
-- automation_run_steps has a cascading foreign key to automation_history. Turn
-- FK enforcement off while rebuilding the parent table so rebuilding history
-- cannot delete or orphan retained Steps.
PRAGMA foreign_keys = OFF;

CREATE TABLE automation_history_new (
    id                            TEXT PRIMARY KEY CHECK (
        (length(id) = 40 AND substr(id, 1, 4) = 'arn_')
        OR (length(id) = 40 AND substr(id, 1, 4) = 'ask_')
    ),
    automation_id                 TEXT NOT NULL CHECK (
        length(automation_id) = 40 AND substr(automation_id, 1, 4) = 'aut_'
    ),
    automation_name               TEXT NOT NULL CHECK (length(automation_name) BETWEEN 1 AND 200),
    kind                          TEXT NOT NULL CHECK (kind IN ('run', 'skip')),
    revision                      INTEGER NOT NULL CHECK (revision >= 1),
    recorded_at                   TEXT NOT NULL,
    fact_id                       TEXT CHECK (
        fact_id IS NULL OR (length(fact_id) = 40 AND substr(fact_id, 1, 4) = 'fct_')
    ),
    fact_family                   TEXT CHECK (
        fact_family IS NULL OR fact_family IN ('observation', 'entity_event')
    ),
    fact_entity_id                TEXT CHECK (
        fact_entity_id IS NULL OR (length(fact_entity_id) = 40 AND substr(fact_entity_id, 1, 4) = 'ent_')
    ),
    fact_variant                  TEXT,
    fact_causation_id             TEXT,
    fact_value_json               TEXT CHECK (
        fact_value_json IS NULL OR json_valid(fact_value_json)
    ),
    fact_emitted_at               TEXT,
    fact_previous_value_json      TEXT CHECK (
        fact_previous_value_json IS NULL OR json_valid(fact_previous_value_json)
    ),
    run_snapshot_json             TEXT CHECK (
        run_snapshot_json IS NULL OR (
            json_valid(run_snapshot_json)
            AND json_type(run_snapshot_json) = 'object'
            AND length(CAST(run_snapshot_json AS BLOB)) <= 65536
        )
    ),
    run_source                    TEXT CHECK (
        run_source IS NULL OR run_source IN ('device_fact', 'manual', 'held_state')
    ),
    run_status                    TEXT CHECK (
        run_status IS NULL OR run_status IN ('running', 'succeeded', 'failed', 'interrupted')
    ),
    run_failure_code              TEXT,
    run_started_at                TEXT,
    run_completed_at              TEXT,
    run_matched_trigger_ids_json  TEXT CHECK (
        run_matched_trigger_ids_json IS NULL OR
            (json_valid(run_matched_trigger_ids_json) AND json_type(run_matched_trigger_ids_json) = 'array')
    ),
    skip_matched_triggers_json    TEXT CHECK (
        skip_matched_triggers_json IS NULL OR
            (json_valid(skip_matched_triggers_json) AND json_type(skip_matched_triggers_json) = 'array')
    ),
    skip_reason                   TEXT CHECK (
        skip_reason IS NULL OR skip_reason IN ('automation_busy', 'stale_fact', 'conditions_false', 'conditions_unknown')
    ),
    skip_source                   TEXT CHECK (
        skip_source IS NULL OR skip_source IN ('device_fact', 'manual', 'held_state')
    ),
    hold_trigger_id               TEXT CHECK (
        hold_trigger_id IS NULL OR (length(hold_trigger_id) BETWEEN 1 AND 63
            AND substr(hold_trigger_id, 1, 1) GLOB '[a-z0-9]'
            AND hold_trigger_id NOT GLOB '*[^a-z0-9_-]*')
    ),
    hold_started_at               TEXT,
    hold_due_at                   TEXT,
    condition_decision_json       TEXT NOT NULL CHECK (
        json_valid(condition_decision_json) AND json_type(condition_decision_json) = 'object'
    ),
    condition_mode                TEXT NOT NULL DEFAULT 'not_configured'
        CHECK (condition_mode IN ('not_configured', 'not_evaluated', 'bypassed', 'evaluated')),
    condition_bypassed            INTEGER NOT NULL DEFAULT 0 CHECK (condition_bypassed IN (0, 1)),
    condition_result              TEXT CHECK (condition_result IS NULL OR condition_result IN ('true', 'false', 'unknown')),
    CHECK (
        (fact_id IS NULL AND fact_family IS NULL AND fact_entity_id IS NULL AND fact_variant IS NULL
            AND fact_causation_id IS NULL AND fact_value_json IS NULL AND fact_emitted_at IS NULL)
        OR (fact_id IS NOT NULL AND fact_family IS NOT NULL AND fact_entity_id IS NOT NULL
            AND fact_variant IS NOT NULL AND fact_causation_id IS NOT NULL AND fact_emitted_at IS NOT NULL
            AND (fact_family <> 'observation' OR fact_value_json IS NOT NULL)
            AND (fact_family <> 'entity_event' OR fact_value_json IS NULL))
    ),
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
                    AND json_array_length(run_matched_trigger_ids_json) = 1)))
        OR (kind = 'skip' AND skip_matched_triggers_json IS NOT NULL AND skip_reason IS NOT NULL AND skip_source IS NOT NULL
            AND ((skip_source = 'device_fact' AND fact_id IS NOT NULL AND hold_trigger_id IS NULL
                    AND json_array_length(skip_matched_triggers_json) > 0)
                OR (skip_source = 'manual' AND fact_id IS NULL AND hold_trigger_id IS NULL
                    AND json_array_length(skip_matched_triggers_json) = 0
                    AND skip_reason IN ('conditions_false', 'conditions_unknown'))
                OR (skip_source = 'held_state' AND fact_id IS NULL AND hold_trigger_id IS NOT NULL
                    AND json_array_length(skip_matched_triggers_json) = 1
                    AND skip_reason IN ('automation_busy', 'conditions_false', 'conditions_unknown')))
            AND run_snapshot_json IS NULL AND run_source IS NULL AND run_status IS NULL AND run_failure_code IS NULL
            AND run_started_at IS NULL AND run_completed_at IS NULL AND run_matched_trigger_ids_json IS NULL)
    )
);

INSERT INTO automation_history_new (
    id, automation_id, automation_name, kind, revision, recorded_at,
    fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id,
    fact_value_json, fact_emitted_at, fact_previous_value_json,
    run_snapshot_json, run_source, run_status, run_failure_code, run_started_at,
    run_completed_at, run_matched_trigger_ids_json, skip_matched_triggers_json,
    skip_reason, skip_source, condition_decision_json, condition_mode,
    condition_bypassed, condition_result
)
SELECT
    id, automation_id, automation_name, kind, revision, recorded_at,
    fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id,
    fact_value_json, fact_emitted_at, fact_previous_value_json,
    run_snapshot_json, run_source, run_status, run_failure_code, run_started_at,
    run_completed_at, run_matched_trigger_ids_json, skip_matched_triggers_json,
    skip_reason, skip_source, condition_decision_json, condition_mode,
    condition_bypassed, condition_result
FROM automation_history;

DROP TABLE automation_history;
ALTER TABLE automation_history_new RENAME TO automation_history;

CREATE INDEX automation_history_page_idx
    ON automation_history(automation_id, recorded_at DESC, id DESC);
CREATE UNIQUE INDEX automation_history_one_running_run_idx
    ON automation_history(automation_id) WHERE kind = 'run' AND run_status = 'running';
CREATE UNIQUE INDEX automation_history_fact_outcome_idx
    ON automation_history(fact_id, automation_id) WHERE fact_id IS NOT NULL;

CREATE TABLE automation_holds (
    automation_id       TEXT NOT NULL REFERENCES automations(id) ON DELETE CASCADE,
    revision            INTEGER NOT NULL CHECK (revision >= 1),
    trigger_id          TEXT NOT NULL,
    last_receive_order  INTEGER NOT NULL CHECK (last_receive_order > 0),
    phase               TEXT NOT NULL CHECK (phase IN ('idle', 'pending', 'consumed')),
    started_at          TEXT,
    due_at              TEXT,
    PRIMARY KEY (automation_id, trigger_id),
    CHECK ((phase = 'pending' AND started_at IS NOT NULL AND due_at IS NOT NULL)
        OR (phase <> 'pending' AND started_at IS NULL AND due_at IS NULL))
);

CREATE INDEX automation_holds_due_idx ON automation_holds(due_at, automation_id, trigger_id)
    WHERE phase = 'pending';

PRAGMA foreign_keys = ON;

-- +goose Down
-- Holds are transient and are safely discarded when rolling back this feature.
PRAGMA foreign_keys = OFF;
DROP TABLE automation_holds;
CREATE TABLE automation_history_old (
    id                            TEXT PRIMARY KEY CHECK (
        (length(id) = 40 AND substr(id, 1, 4) = 'arn_')
        OR (length(id) = 40 AND substr(id, 1, 4) = 'ask_')
    ),
    automation_id                 TEXT NOT NULL CHECK (
        length(automation_id) = 40 AND substr(automation_id, 1, 4) = 'aut_'
    ),
    automation_name               TEXT NOT NULL CHECK (length(automation_name) BETWEEN 1 AND 200),
    kind                          TEXT NOT NULL CHECK (kind IN ('run', 'skip')),
    revision                      INTEGER NOT NULL CHECK (revision >= 1),
    recorded_at                   TEXT NOT NULL,
    fact_id                       TEXT CHECK (
        fact_id IS NULL OR (length(fact_id) = 40 AND substr(fact_id, 1, 4) = 'fct_')
    ),
    fact_family                   TEXT CHECK (
        fact_family IS NULL OR fact_family IN ('observation', 'entity_event')
    ),
    fact_entity_id                TEXT CHECK (
        fact_entity_id IS NULL OR (length(fact_entity_id) = 40 AND substr(fact_entity_id, 1, 4) = 'ent_')
    ),
    fact_variant                  TEXT,
    fact_causation_id             TEXT,
    fact_value_json               TEXT CHECK (fact_value_json IS NULL OR json_valid(fact_value_json)),
    fact_emitted_at               TEXT,
    fact_previous_value_json      TEXT CHECK (
        fact_previous_value_json IS NULL OR json_valid(fact_previous_value_json)
    ),
    run_snapshot_json             TEXT CHECK (
        run_snapshot_json IS NULL OR (
            json_valid(run_snapshot_json)
            AND json_type(run_snapshot_json) = 'object'
            AND length(CAST(run_snapshot_json AS BLOB)) <= 65536
        )
    ),
    run_source                    TEXT CHECK (run_source IS NULL OR run_source IN ('device_fact', 'manual')),
    run_status                    TEXT CHECK (
        run_status IS NULL OR run_status IN ('running', 'succeeded', 'failed', 'interrupted')
    ),
    run_failure_code              TEXT,
    run_started_at                TEXT,
    run_completed_at              TEXT,
    run_matched_trigger_ids_json  TEXT CHECK (
        run_matched_trigger_ids_json IS NULL OR
            (json_valid(run_matched_trigger_ids_json) AND json_type(run_matched_trigger_ids_json) = 'array')
    ),
    skip_matched_triggers_json    TEXT CHECK (
        skip_matched_triggers_json IS NULL OR
            (json_valid(skip_matched_triggers_json) AND json_type(skip_matched_triggers_json) = 'array')
    ),
    skip_reason                   TEXT CHECK (
        skip_reason IS NULL OR skip_reason IN ('automation_busy', 'stale_fact', 'conditions_false', 'conditions_unknown')
    ),
    skip_source                   TEXT CHECK (skip_source IS NULL OR skip_source IN ('device_fact', 'manual')),
    condition_decision_json       TEXT NOT NULL CHECK (
        json_valid(condition_decision_json) AND json_type(condition_decision_json) = 'object'
    ),
    condition_mode                TEXT NOT NULL DEFAULT 'not_configured'
        CHECK (condition_mode IN ('not_configured', 'not_evaluated', 'bypassed', 'evaluated')),
    condition_bypassed            INTEGER NOT NULL DEFAULT 0 CHECK (condition_bypassed IN (0, 1)),
    condition_result              TEXT CHECK (condition_result IS NULL OR condition_result IN ('true', 'false', 'unknown')),
    CHECK (
        (fact_id IS NULL AND fact_family IS NULL AND fact_entity_id IS NULL AND fact_variant IS NULL
            AND fact_causation_id IS NULL AND fact_value_json IS NULL AND fact_emitted_at IS NULL)
        OR (fact_id IS NOT NULL AND fact_family IS NOT NULL AND fact_entity_id IS NOT NULL
            AND fact_variant IS NOT NULL AND fact_causation_id IS NOT NULL AND fact_emitted_at IS NOT NULL
            AND (fact_family <> 'observation' OR fact_value_json IS NOT NULL)
            AND (fact_family <> 'entity_event' OR fact_value_json IS NULL))
    ),
    CHECK (
        (kind = 'run' AND run_snapshot_json IS NOT NULL AND run_source IS NOT NULL
            AND run_status IS NOT NULL AND run_started_at IS NOT NULL AND run_matched_trigger_ids_json IS NOT NULL
            AND skip_matched_triggers_json IS NULL AND skip_reason IS NULL AND skip_source IS NULL
            AND (run_status = 'running') = (run_completed_at IS NULL)
            AND (run_status IN ('failed', 'interrupted')) = (run_failure_code IS NOT NULL)
            AND (run_source = 'device_fact') = (fact_id IS NOT NULL))
        OR (kind = 'skip' AND skip_matched_triggers_json IS NOT NULL AND skip_reason IS NOT NULL AND skip_source IS NOT NULL
            AND ((skip_source = 'device_fact' AND fact_id IS NOT NULL
                    AND json_array_length(skip_matched_triggers_json) > 0)
                OR (skip_source = 'manual' AND fact_id IS NULL
                    AND json_array_length(skip_matched_triggers_json) = 0
                    AND skip_reason IN ('conditions_false', 'conditions_unknown')))
            AND run_snapshot_json IS NULL AND run_source IS NULL AND run_status IS NULL AND run_failure_code IS NULL
            AND run_started_at IS NULL AND run_completed_at IS NULL AND run_matched_trigger_ids_json IS NULL)
    )
);
-- A downgrade cannot represent held-state outcomes under the older provenance
-- checks; deployments should only downgrade after removing those outcomes.
INSERT INTO automation_history_old (
    id, automation_id, automation_name, kind, revision, recorded_at, fact_id, fact_family,
    fact_entity_id, fact_variant, fact_causation_id, fact_value_json, fact_emitted_at,
    fact_previous_value_json, run_snapshot_json, run_source, run_status, run_failure_code,
    run_started_at, run_completed_at, run_matched_trigger_ids_json, skip_matched_triggers_json,
    skip_reason, skip_source, condition_decision_json, condition_mode, condition_bypassed, condition_result
)
SELECT id, automation_id, automation_name, kind, revision, recorded_at, fact_id, fact_family,
    fact_entity_id, fact_variant, fact_causation_id, fact_value_json, fact_emitted_at,
    fact_previous_value_json, run_snapshot_json, run_source, run_status, run_failure_code,
    run_started_at, run_completed_at, run_matched_trigger_ids_json, skip_matched_triggers_json,
    skip_reason, skip_source, condition_decision_json, condition_mode, condition_bypassed, condition_result
FROM automation_history;
DROP TABLE automation_history;
ALTER TABLE automation_history_old RENAME TO automation_history;
CREATE INDEX automation_history_page_idx ON automation_history(automation_id, recorded_at DESC, id DESC);
CREATE UNIQUE INDEX automation_history_one_running_run_idx ON automation_history(automation_id)
    WHERE kind = 'run' AND run_status = 'running';
CREATE UNIQUE INDEX automation_history_fact_outcome_idx ON automation_history(fact_id, automation_id)
    WHERE fact_id IS NOT NULL;
PRAGMA foreign_keys = ON;
