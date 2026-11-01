-- +goose Up
CREATE INDEX observations_temperature_series_idx
    ON observations(entity_id, observed_at, receive_order)
    WHERE disposition IN ('applied', 'unchanged');

-- +goose Down
DROP INDEX observations_temperature_series_idx;
