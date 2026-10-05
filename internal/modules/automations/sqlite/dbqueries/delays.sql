-- name: GetDelayParent :one
SELECT * FROM automation_history WHERE id = ?;

-- name: CreateRunDelay :exec
INSERT INTO automation_run_delays (run_id, step_id, position, status, started_at)
VALUES (?, ?, ?, 'running', ?);

-- name: ListRunDelays :many
SELECT * FROM automation_run_delays WHERE run_id = ? ORDER BY position;

-- name: CompleteRunDelay :execrows
UPDATE automation_run_delays SET status = ?, completed_at = ?, failure_code = ?
WHERE run_id = ? AND step_id = ? AND status = 'running'
    AND EXISTS (SELECT 1 FROM automation_history
        WHERE id = automation_run_delays.run_id AND kind = 'run' AND run_status = 'running');

-- name: InterruptRunningDelays :execrows
UPDATE automation_run_delays SET status = 'interrupted', completed_at = ?, failure_code = ?
WHERE status = 'running';
