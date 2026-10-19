-- +goose Up
CREATE TABLE adapter_function_catalogs (
    adapter_id TEXT PRIMARY KEY REFERENCES adapter_instances(adapter_id),
    catalog_revision TEXT NOT NULL UNIQUE,
    runtime_id TEXT NOT NULL REFERENCES adapter_runtimes(runtime_id),
    registered_at TEXT NOT NULL,
    functions_json TEXT NOT NULL CHECK (json_valid(functions_json))
);

CREATE TABLE adapter_function_invocations (
    id TEXT PRIMARY KEY,
    adapter_id TEXT NOT NULL,
    runtime_id TEXT NOT NULL,
    catalog_revision TEXT NOT NULL,
    function_key TEXT NOT NULL,
    descriptor_json TEXT NOT NULL CHECK (json_valid(descriptor_json)),
    arguments_json TEXT NOT NULL CHECK (json_valid(arguments_json)),
    started_at TEXT NOT NULL,
    deadline TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('running','succeeded','failed','not_executed','unknown')),
    result_json TEXT,
    reason_code TEXT,
    completed_at TEXT,
    CHECK (
        (status = 'running' AND completed_at IS NULL AND result_json IS NULL AND reason_code IS NULL)
        OR (status = 'succeeded' AND completed_at IS NOT NULL
            AND result_json IS NOT NULL AND json_valid(result_json) AND reason_code IS NULL)
        OR (status IN ('failed','not_executed','unknown') AND completed_at IS NOT NULL
            AND result_json IS NULL AND reason_code IS NOT NULL)
    )
);
CREATE INDEX adapter_function_invocations_by_adapter
    ON adapter_function_invocations(adapter_id, started_at DESC, id DESC);
CREATE INDEX adapter_function_invocations_by_function
    ON adapter_function_invocations(adapter_id, function_key, started_at DESC, id DESC);
CREATE INDEX adapter_function_invocations_retention
    ON adapter_function_invocations(completed_at, id) WHERE status != 'running';

-- +goose Down
DROP TABLE adapter_function_invocations;
DROP TABLE adapter_function_catalogs;
