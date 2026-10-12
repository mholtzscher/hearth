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

-- name: InterruptRunningNotifications :exec
UPDATE automation_run_notifications SET status = 'interrupted', completed_at = ?, reason_code = ?
WHERE status = 'running';

-- name: InterruptRunNotifications :exec
UPDATE automation_run_notifications
SET status = 'interrupted', completed_at = ?, reason_code = 'executor_fault'
WHERE run_id = ? AND status = 'running'
    AND EXISTS (SELECT 1 FROM automation_history
        WHERE id = automation_run_notifications.run_id AND kind = 'run' AND run_status = 'running');
