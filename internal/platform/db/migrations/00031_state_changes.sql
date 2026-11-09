-- +goose Up
-- Executed by migration_state_changes.go, followed by the backfill.
CREATE TABLE state_changes (
    entity_id TEXT NOT NULL,
    field_path TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    receive_order INTEGER NOT NULL,
    value_json TEXT NOT NULL CHECK (json_valid(value_json)),
    PRIMARY KEY (entity_id, field_path, observed_at, receive_order)
) WITHOUT ROWID;
CREATE INDEX state_changes_observed_at_idx ON state_changes(observed_at);

-- +goose Down
DROP INDEX state_changes_observed_at_idx;
DROP TABLE state_changes;
