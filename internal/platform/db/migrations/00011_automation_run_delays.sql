-- +goose Up
CREATE TABLE automation_run_delays (
    run_id TEXT NOT NULL REFERENCES automation_history(id) ON DELETE CASCADE,
    step_id TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0 AND position < 64),
    status TEXT NOT NULL CHECK (status IN ('running', 'completed', 'interrupted')),
    started_at TEXT NOT NULL,
    completed_at TEXT,
    failure_code TEXT,
    PRIMARY KEY (run_id, step_id),
    UNIQUE (run_id, position),
    CHECK (
        (status = 'running' AND completed_at IS NULL AND failure_code IS NULL)
        OR (status = 'completed' AND completed_at IS NOT NULL AND failure_code IS NULL)
        OR (status = 'interrupted' AND completed_at IS NOT NULL AND failure_code IS NOT NULL
            AND failure_code IN ('core_stopping', 'core_restarted', 'executor_fault'))
    )
);
CREATE UNIQUE INDEX automation_run_delays_one_running_idx
    ON automation_run_delays(run_id) WHERE status = 'running';

-- +goose Down
DROP TABLE automation_run_delays;
