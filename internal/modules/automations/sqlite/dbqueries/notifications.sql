-- name: CreateRunNotification :exec
INSERT INTO automation_run_notifications (run_id, step_id, position, attempt_id, status, started_at)
VALUES (?, ?, ?, ?, 'running', ?);

-- name: ListRunNotifications :many
SELECT * FROM automation_run_notifications WHERE run_id = ? ORDER BY position;

-- name: CompleteRunNotification :execrows
UPDATE automation_run_notifications SET status = ?, completed_at = ?, outcome = ?, reason_code = ?
WHERE run_id = ? AND step_id = ? AND attempt_id = ? AND status = 'running'
    AND EXISTS (SELECT 1 FROM automation_history
        WHERE id = automation_run_notifications.run_id AND kind = 'run' AND run_status = 'running');

-- name: InterruptNotifications :exec
UPDATE automation_run_notifications
SET status = 'interrupted', completed_at = sqlc.narg(completed_at), reason_code = sqlc.narg(reason)
WHERE status = 'running' AND ((run_id = sqlc.arg(scope)
    AND EXISTS (SELECT 1 FROM automation_history
        WHERE id = automation_run_notifications.run_id AND kind = 'run' AND run_status = 'running')) OR sqlc.arg(scope) = '');
