-- +goose Up
-- Executed by migration_observation_rollups.go, followed by the exact backfill.
CREATE TABLE observation_rollups (
    entity_id TEXT NOT NULL,
    field_path TEXT NOT NULL,
    cell_start INTEGER NOT NULL CHECK (cell_start % 60 = 0),
    sample_count INTEGER NOT NULL CHECK (sample_count >= 0),
    null_count INTEGER NOT NULL CHECK (null_count >= 0),
    invalid_count INTEGER NOT NULL CHECK (invalid_count >= 0),
    sum_value TEXT NOT NULL,
    min_value,
    max_value,
    CHECK ((sample_count = 0) = (min_value IS NULL)
       AND (sample_count = 0) = (max_value IS NULL)),
    PRIMARY KEY (entity_id, field_path, cell_start)
) WITHOUT ROWID;
CREATE INDEX observation_rollups_cell_start_idx ON observation_rollups(cell_start);

-- +goose Down
CREATE INDEX observations_entity_observed_history_idx
    ON observations(entity_id, observed_at, receive_order)
    WHERE disposition IN ('applied', 'unchanged');
DROP INDEX observation_rollups_cell_start_idx;
DROP TABLE observation_rollups;
