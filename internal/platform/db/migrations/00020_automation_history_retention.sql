-- +goose Up
CREATE INDEX automation_history_retention_idx
ON automation_history(recorded_at, id)
WHERE kind = 'skip' OR run_status <> 'running';

-- +goose Down
DROP INDEX automation_history_retention_idx;
