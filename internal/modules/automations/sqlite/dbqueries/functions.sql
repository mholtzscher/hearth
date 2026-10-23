-- name: CreateRunFunction :exec
INSERT INTO automation_run_functions (run_id, step_id, position, invocation_id, started_at, status)
VALUES (?, ?, ?, ?, ?, 'running');

-- name: ListRunFunctions :many
SELECT * FROM automation_run_functions WHERE run_id = ? ORDER BY position;

-- name: CompleteRunFunction :execrows
UPDATE automation_run_functions
SET status = ?, completed_at = ?, catalog_revision = ?, invocation_outcome = ?, reason_code = ?
WHERE run_id = ? AND step_id = ? AND invocation_id = ? AND status = 'running'
    AND EXISTS (SELECT 1 FROM automation_history
        WHERE id = automation_run_functions.run_id AND kind = 'run' AND run_status = 'running');

-- name: InterruptRunFunctions :exec
UPDATE automation_run_functions
SET status = 'interrupted', completed_at = ?, reason_code = 'executor_fault'
WHERE run_id = ? AND status = 'running'
    AND EXISTS (SELECT 1 FROM automation_history
        WHERE id = automation_run_functions.run_id AND kind = 'run' AND run_status = 'running');

-- name: InterruptRunningFunctions :exec
UPDATE automation_run_functions
SET status = 'interrupted', completed_at = ?, reason_code = ?
WHERE status = 'running';
