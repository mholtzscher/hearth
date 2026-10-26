-- Persist global UTC schedule progress without replay.

-- name: GetScheduleWatermark :one
SELECT highwater_at FROM automation_schedule_watermarks WHERE id = 'global';

-- name: AdvanceScheduleWatermark :exec
INSERT INTO automation_schedule_watermarks (id, highwater_at)
VALUES ('global', ?)
ON CONFLICT (id) DO UPDATE
SET highwater_at = MAX(highwater_at, excluded.highwater_at);

-- Store normalized definition documents under optimistic revision control.

-- name: CreateDefinitionRevision :exec
INSERT INTO automation_definition_revisions (automation_id, revision, definition_json)
VALUES (?, ?, ?);

-- name: GetDefinitionRevision :one
SELECT definition_json FROM automation_definition_revisions
WHERE automation_id = ? AND revision = ?;

-- name: CreateAutomation :exec
INSERT INTO automations (id, revision, created_at, updated_at)
VALUES (?, 1, ?, ?);

-- name: GetAutomation :one
SELECT id, revision, definition_json, created_at, updated_at
FROM automation_definitions
WHERE id = ?;

-- name: ListAutomationsFirstPage :many
SELECT id, revision, definition_json, created_at, updated_at
FROM automation_definitions
ORDER BY id
LIMIT ?;

-- name: ListAutomationsAfter :many
SELECT id, revision, definition_json, created_at, updated_at
FROM automation_definitions
WHERE id > ?
ORDER BY id
LIMIT ?;

-- name: ReplaceAutomation :exec
UPDATE automations
SET revision = revision + 1,
    updated_at = ?
WHERE id = ? AND revision = ?;

-- name: DeleteAutomationHolds :exec
DELETE FROM automation_holds
WHERE automation_id = ?;

-- Held-state cursor and deadline transitions run inside automation-owned transactions.

-- name: GetHeldStateObservationReceiveOrder :one
SELECT receive_order FROM observations WHERE observation_id = ?;

-- name: StartHeldStateFact :exec
INSERT INTO automation_holds (
    automation_id, revision, trigger_id, last_receive_order, phase, started_at, due_at
) VALUES (?, ?, ?, ?, 'pending', ?, ?)
ON CONFLICT (automation_id, trigger_id) DO UPDATE SET
    revision = excluded.revision,
    last_receive_order = excluded.last_receive_order,
    phase = CASE WHEN automation_holds.phase = 'idle' THEN 'pending'
        ELSE automation_holds.phase END,
    started_at = CASE WHEN automation_holds.phase = 'idle' THEN excluded.started_at
        ELSE automation_holds.started_at END,
    due_at = CASE WHEN automation_holds.phase = 'idle' THEN excluded.due_at
        ELSE automation_holds.due_at END
WHERE excluded.last_receive_order > automation_holds.last_receive_order;

-- name: AdvanceStaleHeldStateFact :exec
INSERT INTO automation_holds (automation_id, revision, trigger_id, last_receive_order, phase)
VALUES (?, ?, ?, ?, 'idle')
ON CONFLICT (automation_id, trigger_id) DO UPDATE SET
    revision = excluded.revision,
    last_receive_order = excluded.last_receive_order
WHERE excluded.last_receive_order > automation_holds.last_receive_order;

-- name: CancelHeldStateFact :exec
INSERT INTO automation_holds (automation_id, revision, trigger_id, last_receive_order, phase)
VALUES (?, ?, ?, ?, 'idle')
ON CONFLICT (automation_id, trigger_id) DO UPDATE SET
    revision = excluded.revision,
    last_receive_order = excluded.last_receive_order,
    phase = 'idle', started_at = NULL, due_at = NULL
WHERE excluded.last_receive_order > automation_holds.last_receive_order;

-- name: ListDueHeldStateCandidates :many
SELECT automation_id, revision, trigger_id, due_at
FROM automation_holds WHERE phase = 'pending' AND due_at <= ?
ORDER BY due_at, automation_id, trigger_id LIMIT ?;

-- name: ListDueHeldStateRows :many
SELECT automation_id, revision, trigger_id, last_receive_order, phase, started_at, due_at
FROM automation_holds WHERE phase = 'pending' AND due_at <= ?
ORDER BY due_at, automation_id, trigger_id LIMIT ?;

-- name: GetHeldStateEntityState :one
SELECT value_json, receive_order FROM entity_states WHERE entity_id = ?;

-- name: ConsumeDueHeldState :exec
UPDATE automation_holds SET phase = 'consumed', started_at = NULL, due_at = NULL,
    last_receive_order = MAX(last_receive_order, ?)
WHERE automation_id = ? AND trigger_id = ? AND revision = ? AND phase = 'pending'
    AND due_at <= ?;

-- name: CancelHeldStateWithoutState :exec
UPDATE automation_holds SET phase = 'idle', started_at = NULL, due_at = NULL
WHERE automation_id = ? AND trigger_id = ?;

-- name: CancelHeldStateWithReceiveOrder :exec
UPDATE automation_holds SET phase = 'idle', started_at = NULL, due_at = NULL,
    last_receive_order = MAX(last_receive_order, ?)
WHERE automation_id = ? AND trigger_id = ?;

-- name: DeleteHeldState :exec
DELETE FROM automation_holds WHERE automation_id = ? AND trigger_id = ?;

-- name: ResetPendingHeldStates :exec
UPDATE automation_holds SET phase = 'idle', started_at = NULL, due_at = NULL
WHERE phase = 'pending';

-- name: DeleteAutomation :execrows
DELETE FROM automations
WHERE id = ? AND revision = ?;

-- Runtime admission, execution, and history queries use automation-owned transactions.

-- Exclude only explicit false; malformed enablement must still reach the decoder.
-- name: ListEnabledAutomations :many
SELECT id, revision, definition_json, created_at, updated_at
FROM automation_definitions
WHERE json_type(definition_json, '$.enabled') IS NOT 'false'
ORDER BY id;

-- name: CountRunningRuns :one
SELECT count(*) FROM automation_history
WHERE automation_id = ? AND kind = 'run' AND run_status = 'running';

-- name: CountFactReceipts :one
SELECT count(*) FROM automation_fact_receipts
WHERE fact_id = ? AND automation_id = ?;

-- name: CreateHistoryRun :exec
INSERT INTO automation_history (
    id, automation_id, automation_name, kind, revision, recorded_at,
    fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id,
    fact_value_json, fact_previous_value_json, fact_emitted_at,
    hold_trigger_id, hold_started_at, hold_due_at,
    definition_revision, run_source, run_status, run_started_at,
    run_matched_trigger_ids_json,
    condition_mode, condition_bypassed, condition_result, condition_decision_json
) VALUES (?, ?, ?, 'run', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'running', ?, ?, ?, ?, ?, ?);

-- name: CreateHistorySkip :exec
INSERT INTO automation_history (
    id, automation_id, automation_name, kind, revision, recorded_at,
    fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id,
    fact_value_json, fact_previous_value_json, fact_emitted_at, skip_matched_triggers_json, skip_reason,
    skip_source, hold_trigger_id, hold_started_at, hold_due_at,
    condition_mode, condition_bypassed, condition_result, condition_decision_json
) VALUES (?, ?, ?, 'skip', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CreateFactReceipt :exec
INSERT INTO automation_fact_receipts (fact_id, automation_id, outcome_kind, history_id)
VALUES (?, ?, ?, ?);

-- An empty scope selects startup recovery; a Run ID selects operation-scoped cleanup.
-- name: InterruptRuns :exec
UPDATE automation_history
SET run_status = 'interrupted', run_failure_code = sqlc.narg(reason), run_completed_at = sqlc.narg(completed_at)
WHERE kind = 'run' AND run_status = 'running' AND (id = sqlc.arg(scope) OR sqlc.arg(scope) = '');

-- name: GetHistoryEntry :one
SELECT * FROM automation_history
WHERE automation_id = ? AND id = ?;

-- name: ListHistoryFirstPage :many
SELECT id, automation_id, automation_name, kind, revision, recorded_at,
    run_status, run_source, skip_reason, skip_source,
    fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id,
    fact_value_json, fact_previous_value_json, fact_emitted_at,
    hold_trigger_id, hold_started_at, hold_due_at,
    condition_mode, condition_result, condition_bypassed
FROM automation_history
WHERE automation_id = ?
ORDER BY recorded_at DESC, id DESC
LIMIT ?;

-- name: ListHistoryAfter :many
SELECT id, automation_id, automation_name, kind, revision, recorded_at,
    run_status, run_source, skip_reason, skip_source,
    fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id,
    fact_value_json, fact_previous_value_json, fact_emitted_at,
    hold_trigger_id, hold_started_at, hold_due_at,
    condition_mode, condition_result, condition_bypassed
FROM automation_history
WHERE automation_id = sqlc.arg(automation_id)
  AND (recorded_at, id) < (sqlc.arg(recorded_at), sqlc.arg(id))
ORDER BY recorded_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: DeleteHistoryBefore :execrows
DELETE FROM automation_history
WHERE id IN (
    SELECT candidate.id FROM automation_history AS candidate
    WHERE candidate.recorded_at < ?
      AND (candidate.kind = 'skip' OR candidate.run_status <> 'running')
    ORDER BY candidate.recorded_at
    LIMIT ?
);

-- name: DeleteUnreferencedDefinitionRevisions :exec
DELETE FROM automation_definition_revisions
WHERE (automation_id, revision) IN (
    SELECT artifact.automation_id, artifact.revision
    FROM automation_definition_revisions AS artifact
    WHERE NOT EXISTS (SELECT 1 FROM automations AS head
        WHERE head.id = artifact.automation_id AND head.revision = artifact.revision)
      AND NOT EXISTS (SELECT 1 FROM automation_history AS history
        WHERE history.automation_id = artifact.automation_id
          AND history.definition_revision = artifact.revision)
    ORDER BY artifact.automation_id, artifact.revision
    LIMIT ?
);
