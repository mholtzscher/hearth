-- +goose Up
CREATE TABLE automation_run_notifications (
    run_id TEXT NOT NULL REFERENCES automation_history(id) ON DELETE CASCADE,
    step_id TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0 AND position < 32),
    attempt_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('running', 'accepted', 'failed', 'interrupted')),
    started_at TEXT NOT NULL,
    completed_at TEXT,
    outcome TEXT CHECK (outcome IN ('accepted', 'rejected', 'not_sent', 'unknown')),
    reason_code TEXT,
    PRIMARY KEY (run_id, step_id),
    UNIQUE (run_id, position),
    CHECK (
        (status = 'running' AND completed_at IS NULL AND outcome IS NULL AND reason_code IS NULL)
        OR (status = 'accepted' AND completed_at IS NOT NULL AND outcome IS NOT NULL AND outcome = 'accepted' AND reason_code IS NULL)
        OR (status = 'failed' AND completed_at IS NOT NULL AND reason_code IS NOT NULL AND (
            (outcome IS NOT NULL AND outcome IN ('rejected', 'not_sent', 'unknown'))
            OR (outcome IS NULL AND reason_code IN ('destination_not_found', 'notification_admission_closed'))))
        OR (status = 'interrupted' AND completed_at IS NOT NULL AND outcome IS NULL AND reason_code IS NOT NULL
            AND reason_code IN ('core_stopping', 'core_restarted', 'executor_fault'))
    )
);
CREATE UNIQUE INDEX automation_run_notifications_one_running_idx
    ON automation_run_notifications(run_id) WHERE status = 'running';

-- +goose Down
DROP TABLE automation_run_notifications;
