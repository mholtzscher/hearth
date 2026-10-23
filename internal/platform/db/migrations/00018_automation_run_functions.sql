-- +goose Up
CREATE TABLE automation_run_functions (
    run_id TEXT NOT NULL REFERENCES automation_history(id) ON DELETE CASCADE,
    step_id TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 31),
    invocation_id TEXT NOT NULL UNIQUE,
    started_at TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'interrupted')),
    completed_at TEXT,
    catalog_revision TEXT,
    invocation_outcome TEXT,
    reason_code TEXT,
    PRIMARY KEY (run_id, step_id),
    UNIQUE (run_id, position),
    CHECK (completed_at IS NULL OR completed_at >= started_at),
    CHECK (
        (status = 'running' AND completed_at IS NULL AND catalog_revision IS NULL
            AND invocation_outcome IS NULL AND reason_code IS NULL)
        OR (status = 'succeeded' AND completed_at IS NOT NULL AND catalog_revision IS NOT NULL
            AND invocation_outcome IS NOT NULL AND invocation_outcome = 'succeeded' AND reason_code IS NULL)
        OR (status = 'failed' AND completed_at IS NOT NULL AND reason_code IS NOT NULL
            AND length(reason_code) BETWEEN 1 AND 128 AND (
                (catalog_revision IS NULL AND invocation_outcome IS NULL AND reason_code IN (
                    'adapter_not_found', 'function_not_found', 'function_contract_changed', 'catalog_changed',
                    'adapter_unavailable', 'invalid_function_arguments', 'function_admission_closed'))
                OR (catalog_revision IS NOT NULL AND invocation_outcome IS NOT NULL
                    AND invocation_outcome IN ('failed', 'not_executed', 'unknown'))))
        OR (status = 'interrupted' AND completed_at IS NOT NULL AND catalog_revision IS NULL
            AND invocation_outcome IS NULL AND reason_code IS NOT NULL
            AND reason_code IN ('core_stopping', 'core_restarted', 'executor_fault'))
    )
);

CREATE UNIQUE INDEX automation_run_functions_one_running_idx
    ON automation_run_functions(run_id) WHERE status = 'running';

-- +goose Down
DROP TABLE automation_run_functions;
