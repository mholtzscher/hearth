-- +goose Up
CREATE INDEX observations_entity_observed_history_idx
    ON observations(entity_id, observed_at, receive_order)
    WHERE disposition IN ('applied', 'unchanged');

-- +goose Down
DROP INDEX observations_entity_observed_history_idx;
