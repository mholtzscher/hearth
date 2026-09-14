-- Store normalized definition documents under optimistic revision control.

-- name: CreateAutomation :one
INSERT INTO automations (id, revision, definition_json, created_at, updated_at)
VALUES (?, 1, ?, ?, ?)
RETURNING id, revision, definition_json, created_at, updated_at;

-- name: GetAutomation :one
SELECT id, revision, definition_json, created_at, updated_at
FROM automations
WHERE id = ?;

-- name: ListAutomationsFirstPage :many
SELECT id, revision, definition_json, created_at, updated_at
FROM automations
ORDER BY id
LIMIT ?;

-- name: ListAutomationsAfter :many
SELECT id, revision, definition_json, created_at, updated_at
FROM automations
WHERE id > ?
ORDER BY id
LIMIT ?;

-- name: ReplaceAutomation :one
UPDATE automations
SET revision = revision + 1,
    definition_json = ?,
    updated_at = ?
WHERE id = ? AND revision = ?
RETURNING id, revision, definition_json, created_at, updated_at;

-- name: DeleteAutomation :execrows
DELETE FROM automations
WHERE id = ? AND revision = ?;

-- Runtime admission, execution, and history queries use automation-owned transactions.

-- name: ListAllAutomations :many
SELECT id, revision, definition_json, created_at, updated_at
FROM automations
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
    fact_value_json, fact_emitted_at,
    run_snapshot_json, run_source, run_status, run_started_at,
    run_matched_trigger_ids_json
) VALUES (?, ?, ?, 'run', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'running', ?, ?);

-- name: CreateHistorySkip :exec
INSERT INTO automation_history (
    id, automation_id, automation_name, kind, revision, recorded_at,
    fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id,
    fact_value_json, fact_emitted_at, skip_matched_triggers_json, skip_reason
) VALUES (?, ?, ?, 'skip', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CreateRunStep :exec
INSERT INTO automation_run_steps (run_id, position, step_id, status)
VALUES (?, ?, ?, 'not_attempted');

-- name: CreateFactReceipt :exec
INSERT INTO automation_fact_receipts (fact_id, automation_id, outcome_kind, history_id)
VALUES (?, ?, ?, ?);

-- name: MarkStepRunning :execrows
UPDATE automation_run_steps
SET status = 'running',
    reserved_command_id = ?,
    reserved_correlation_id = ?,
    started_at = ?
WHERE run_id = ? AND position = ? AND status = 'not_attempted';

-- name: CompleteStep :execrows
UPDATE automation_run_steps
SET status = ?,
    verified_command_id = ?,
    failure_code = ?,
    started_at = COALESCE(started_at, ?),
    completed_at = ?
WHERE run_id = ? AND position = ? AND status IN ('running', 'not_attempted');

-- name: CompleteRun :execrows
UPDATE automation_history
SET run_status = ?,
    run_failure_code = ?,
    run_completed_at = ?
WHERE id = ? AND kind = 'run' AND run_status = 'running';

-- name: InterruptRunningRuns :execrows
UPDATE automation_history
SET run_status = 'interrupted',
    run_failure_code = ?,
    run_completed_at = ?
WHERE kind = 'run' AND run_status = 'running';

-- name: InterruptRunningSteps :execrows
UPDATE automation_run_steps
SET status = 'interrupted',
    failure_code = ?,
    completed_at = ?
WHERE status = 'running';

-- name: GetHistoryEntry :one
SELECT * FROM automation_history
WHERE automation_id = ? AND id = ?;

-- name: ListHistoryFirstPage :many
SELECT * FROM automation_history
WHERE automation_id = ?
ORDER BY recorded_at DESC, id DESC
LIMIT ?;

-- name: ListHistoryAfter :many
SELECT * FROM automation_history
WHERE automation_id = ?
  AND (recorded_at < ? OR (recorded_at = ? AND id < ?))
ORDER BY recorded_at DESC, id DESC
LIMIT ?;

-- name: ListRunSteps :many
SELECT * FROM automation_run_steps
WHERE run_id = ?
ORDER BY position;

-- name: DeleteHistoryBefore :execrows
DELETE FROM automation_history
WHERE id IN (
    SELECT candidate.id FROM automation_history AS candidate
    WHERE candidate.recorded_at < ?
      AND (candidate.kind = 'skip' OR candidate.run_status <> 'running')
    ORDER BY candidate.recorded_at
    LIMIT ?
);
