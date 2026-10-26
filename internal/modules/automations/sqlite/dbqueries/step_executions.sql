-- name: CreateStepExecution :exec
INSERT INTO automation_step_executions (
    run_id, step_id, sequence, kind, phase, started_at, completed_at, evidence_json
) VALUES (
    sqlc.arg(run_id), sqlc.arg(step_id),
    (SELECT COALESCE(MAX(sequence), -1) + 1 FROM automation_step_executions WHERE run_id = sqlc.arg(run_id)),
    sqlc.arg(kind), sqlc.arg(phase), sqlc.arg(started_at), sqlc.narg(completed_at), sqlc.arg(evidence_json)
);

-- name: GetStepExecution :one
SELECT run_id, step_id, sequence, kind, phase, started_at, completed_at, evidence_json
FROM automation_step_executions WHERE run_id = ? AND step_id = ?;

-- name: ListStepExecutions :many
SELECT run_id, step_id, sequence, kind, phase, started_at, completed_at, evidence_json
FROM automation_step_executions WHERE run_id = ? ORDER BY sequence;

-- name: CountRunningStepExecutions :one
SELECT count(*) FROM automation_step_executions WHERE run_id = ? AND phase = 'running';

-- Control reads never fetch prior Command, provider, Function, or Condition evidence.
-- name: ListStepExecutionControl :many
SELECT step_id, sequence, kind, phase,
    CASE WHEN kind IN ('if', 'choose') THEN json_extract(evidence_json, '$.outcome') END AS branch_outcome,
    CASE WHEN kind = 'choose' THEN json_extract(evidence_json, '$.selected_branch_id') END AS selected_branch_id
FROM automation_step_executions WHERE run_id = ? ORDER BY sequence;

-- name: GetExecutionParent :one
SELECT history.id, history.automation_id, history.kind, history.run_status, history.run_started_at,
    history.run_matched_trigger_ids_json, artifact.definition_json
FROM automation_history AS history
LEFT JOIN automation_definition_revisions AS artifact
    ON artifact.automation_id = history.automation_id AND artifact.revision = history.definition_revision
WHERE history.id = ?;

-- name: CompleteStepExecution :execrows
UPDATE automation_step_executions
SET phase = ?, completed_at = ?, evidence_json = ?
WHERE run_id = ? AND step_id = ? AND phase = 'running'
    AND EXISTS (SELECT 1 FROM automation_history
        WHERE id = automation_step_executions.run_id AND kind = 'run' AND run_status = 'running');

-- name: CompleteExecutionParent :execrows
UPDATE automation_history
SET run_status = sqlc.arg(status), run_failure_code = sqlc.narg(reason), run_completed_at = sqlc.arg(completed_at)
WHERE id = sqlc.arg(run_id) AND kind = 'run' AND run_status = 'running'
    AND NOT EXISTS (SELECT 1 FROM automation_step_executions
        WHERE run_id = automation_history.id AND phase = 'running')
    AND (sqlc.arg(status) <> 'succeeded' OR NOT EXISTS (
        SELECT 1 FROM automation_step_executions
        WHERE run_id = automation_history.id AND phase IN ('failed', 'interrupted')));

-- name: InterruptStepExecutions :exec
UPDATE automation_step_executions
SET phase = 'interrupted', completed_at = sqlc.arg(completed_at),
    evidence_json = json_set(evidence_json, '$.reason_code', sqlc.arg(reason))
WHERE phase = 'running' AND (sqlc.arg(scope) = '' OR (run_id = sqlc.arg(scope)
    AND EXISTS (SELECT 1 FROM automation_history
        WHERE id = automation_step_executions.run_id AND kind = 'run' AND run_status = 'running')));
