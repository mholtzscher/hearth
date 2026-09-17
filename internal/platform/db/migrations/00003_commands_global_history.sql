-- +goose Up
-- Global Command history reads newest-first across all Entities, so the
-- household-wide list needs its own position index. The existing
-- commands_entity_requested_idx keeps serving per-Entity history.
CREATE INDEX commands_requested_idx
    ON commands(requested_at DESC, id DESC);

-- +goose Down
DROP INDEX commands_requested_idx;
