-- +goose Up
-- Lifecycle transitions and positions establish order; wall times are diagnostic.
CREATE TABLE automation_run_functions_new (
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
INSERT INTO automation_run_functions_new (
    run_id, step_id, position, invocation_id, started_at, status,
    completed_at, catalog_revision, invocation_outcome, reason_code
)
SELECT run_id, step_id, position, invocation_id, started_at, status,
    completed_at, catalog_revision, invocation_outcome, reason_code
FROM automation_run_functions;
DROP TABLE automation_run_functions;
ALTER TABLE automation_run_functions_new RENAME TO automation_run_functions;
CREATE UNIQUE INDEX automation_run_functions_one_running_idx
    ON automation_run_functions(run_id) WHERE status = 'running';

-- +goose Down
-- Reinstating chronology fails transactionally if retained rows cannot satisfy it.
-- Never clamp or discard diagnostic evidence to make a downgrade succeed.
CREATE TABLE automation_run_functions_old (
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
INSERT INTO automation_run_functions_old (
    run_id, step_id, position, invocation_id, started_at, status,
    completed_at, catalog_revision, invocation_outcome, reason_code
)
SELECT run_id, step_id, position, invocation_id, started_at, status,
    completed_at, catalog_revision, invocation_outcome, reason_code
FROM automation_run_functions;
DROP TABLE automation_run_functions;
ALTER TABLE automation_run_functions_old RENAME TO automation_run_functions;
CREATE UNIQUE INDEX automation_run_functions_one_running_idx
    ON automation_run_functions(run_id) WHERE status = 'running';
