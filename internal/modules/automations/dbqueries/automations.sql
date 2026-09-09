-- name: CreateAutomation :one
INSERT INTO automations (id, revision, name, enabled, triggers_json, steps_json, created_at, updated_at)
VALUES (?, 1, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: GetAutomation :one
SELECT * FROM automations WHERE id = ?;

-- name: ListAutomations :many
SELECT * FROM automations WHERE id > sqlc.arg(after_id) ORDER BY id LIMIT sqlc.arg(page_limit);

-- name: UpdateAutomation :one
UPDATE automations SET revision = revision + 1, name = ?, enabled = ?, triggers_json = ?, steps_json = ?, updated_at = ?
WHERE id = ? AND revision = sqlc.arg(expected_revision) RETURNING *;

-- name: DeleteAutomation :execrows
DELETE FROM automations WHERE id = ? AND revision = ?;

-- name: GetActiveAutomationRun :one
SELECT id FROM automation_runs WHERE automation_id = ? AND status = 'running';

-- name: GetManualAutomationRun :one
SELECT * FROM automation_runs WHERE automation_id = ? AND idempotency_key = ?;

-- name: CreateAutomationRun :exec
INSERT INTO automation_runs (id, automation_id, revision, snapshot_json, source, scheduled_at, matched_trigger_ids_json, status, started_at, idempotency_key)
VALUES (?, ?, ?, ?, ?, ?, ?, 'running', ?, ?);

-- name: CreateAutomationRunStep :exec
INSERT INTO automation_run_steps (run_id, step_index, definition_json, status) VALUES (?, ?, ?, 'pending');

-- name: GetAutomationRun :one
SELECT * FROM automation_runs WHERE id = ?;

-- name: ListAutomationRuns :many
SELECT * FROM automation_runs
WHERE (sqlc.narg(automation_filter) IS NULL OR automation_id = sqlc.narg(automation_filter))
AND (sqlc.narg(before_time) IS NULL OR (started_at, id) < (sqlc.narg(before_time), sqlc.narg(before_id)))
ORDER BY started_at DESC, id DESC LIMIT sqlc.arg(page_limit);

-- name: ListRunningAutomationRuns :many
SELECT * FROM automation_runs WHERE status = 'running' ORDER BY id;

-- name: ListAutomationRunSteps :many
SELECT run_id, step_index, definition_json, status, reserved_command_id, reserved_correlation_id, outcome, failure_code, precreation_failure, started_at, completed_at FROM automation_run_steps WHERE run_id = ? ORDER BY step_index;

-- Command candidates are deliberately private. The module's one ownership predicate
-- must verify both identities and the persisted pre-creation failure marker before
-- exposing evidence.
-- name: GetAutomationCommandCandidate :one
SELECT id, correlation_id, status FROM commands WHERE id = ?;

-- name: BeginAutomationStep :execrows
UPDATE automation_run_steps SET status = 'running', reserved_command_id = ?, reserved_correlation_id = ?, started_at = ?
WHERE automation_run_steps.run_id = ? AND automation_run_steps.step_index = ? AND automation_run_steps.status = 'pending'
AND EXISTS (SELECT 1 FROM automation_runs WHERE id = automation_run_steps.run_id AND status = 'running')
AND NOT EXISTS (SELECT 1 FROM automation_run_steps AS prior WHERE prior.run_id = automation_run_steps.run_id
    AND prior.step_index < automation_run_steps.step_index AND prior.status NOT IN ('satisfied', 'dispatched'));

-- name: CompleteAutomationStep :execrows
UPDATE automation_run_steps SET status = ?, outcome = ?, failure_code = ?, precreation_failure = ?, completed_at = ?
WHERE run_id = ? AND step_index = ? AND status = 'running'
AND EXISTS (SELECT 1 FROM automation_runs WHERE id = automation_run_steps.run_id AND status = 'running');

-- name: SkipPendingAutomationSteps :exec
UPDATE automation_run_steps SET status = 'not_attempted', completed_at = ? WHERE run_id = ? AND status = 'pending';

-- name: InterruptRunningAutomationSteps :exec
UPDATE automation_run_steps SET status = 'interrupted', failure_code = ?, completed_at = ? WHERE run_id = ? AND status = 'running';

-- name: CompleteAutomationRun :execrows
UPDATE automation_runs SET status = ?, completed_at = ?, failure_code = ? WHERE id = ? AND status = 'running';

-- name: PruneAutomationHistory :execrows
DELETE FROM automation_runs WHERE id IN (
 SELECT id FROM automation_runs WHERE automation_runs.status <> 'running' AND automation_runs.completed_at < sqlc.arg(cutoff)
 ORDER BY automation_runs.completed_at, automation_runs.id LIMIT 500
);
