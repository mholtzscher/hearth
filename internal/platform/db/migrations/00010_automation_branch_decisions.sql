-- +goose Up
CREATE TABLE automation_run_branch_decisions (
    run_id TEXT NOT NULL REFERENCES automation_history(id) ON DELETE CASCADE,
    step_id TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0 AND position < 64),
    decision_json TEXT NOT NULL
        CHECK (json_valid(decision_json) AND json_type(decision_json) = 'object'),
    PRIMARY KEY (run_id, step_id),
    UNIQUE (run_id, position)
);

-- +goose Down
-- Down drops decision evidence only. Binary rollback requires a pre-feature backup.
DROP TABLE automation_run_branch_decisions;
