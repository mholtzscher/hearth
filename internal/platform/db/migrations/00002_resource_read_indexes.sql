-- +goose Up
UPDATE commands
SET requested_at = CASE
    WHEN length(requested_at) = 20
         AND substr(requested_at, 20, 1) = 'Z'
        THEN substr(requested_at, 1, 19) || '.000000000Z'
    WHEN length(requested_at) BETWEEN 22 AND 30
         AND substr(requested_at, 20, 1) = '.'
         AND substr(requested_at, -1) = 'Z'
        THEN substr(requested_at, 1, length(requested_at) - 1)
             || substr('000000000', 1, 30 - length(requested_at)) || 'Z'
    ELSE requested_at
END;

CREATE INDEX entities_device_id_idx ON entities(device_id, id);
DROP INDEX commands_entity_requested_idx;
CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC, id DESC);

-- +goose Down
DROP INDEX commands_entity_requested_idx;
CREATE INDEX commands_entity_requested_idx
    ON commands(entity_id, requested_at DESC);
DROP INDEX entities_device_id_idx;
