-- +goose Up
CREATE INDEX entities_device_id_idx ON entities(device_id, id);
DROP INDEX commands_entity_requested_idx;
CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC, id DESC);

-- +goose Down
DROP INDEX commands_entity_requested_idx;
CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC);
DROP INDEX entities_device_id_idx;
