-- +goose Up
-- One row per Entity, catalogued numeric State field, and one-minute cell of
-- accepted (applied or unchanged) reports. Numeric history summaries read only
-- these rows.
CREATE TABLE observation_rollups (
    entity_id     TEXT    NOT NULL,
    field_path    TEXT    NOT NULL CHECK (field_path = '' OR field_path GLOB '/[a-z]*'),
    -- Unix seconds, UTC, on the one-minute grid.
    cell_start    INTEGER NOT NULL CHECK (cell_start % 60 = 0),
    sample_count  INTEGER NOT NULL CHECK (sample_count >= 0),
    null_count    INTEGER NOT NULL CHECK (null_count >= 0),
    invalid_count INTEGER NOT NULL CHECK (invalid_count >= 0),
    -- Untyped columns keep SQLite's INTEGER/REAL storage class, which mirrors
    -- the int64/float64 split of JSON integer and decimal tokens.
    sum_value,
    min_value,
    max_value,
    CHECK ((sample_count = 0) = (sum_value IS NULL)
       AND (sample_count = 0) = (min_value IS NULL)
       AND (sample_count = 0) = (max_value IS NULL)),
    PRIMARY KEY (entity_id, field_path, cell_start)
) WITHOUT ROWID;

CREATE INDEX observation_rollups_cell_start_idx ON observation_rollups(cell_start);

-- Only the removed expression history query read through this index.
DROP INDEX observations_entity_observed_history_idx;

-- Backfill from retained accepted reports. The field list is frozen as of this
-- migration and mirrors the generated NumericField catalog entries; a devices
-- test fails when the catalog gains a numeric field no backfill covers.
-- rollup-fields-begin
WITH fields(type_id, field_path, json_path) AS (VALUES
    ('hearth.brightness/v1', '', '$'),
    ('hearth.colorhs/v1', '/hue', '$.hue'),
    ('hearth.colorhs/v1', '/saturation', '$.saturation'),
    ('hearth.colortemp/v1', '/value', '$.value'),
    ('hearth.colorxy/v1', '/x', '$.x'),
    ('hearth.colorxy/v1', '/y', '$.y'),
    ('hearth.numericsensor/v1', '', '$'),
    ('hearth.numericsetting/v1', '/value', '$.value'),
    ('hearth.pressure/v1', '', '$'),
    ('hearth.relativehumidity/v1', '', '$'),
    ('hearth.speed/v1', '', '$'),
    ('hearth.temperature/v1', '', '$')),
-- rollup-fields-end
samples AS (
    SELECT o.entity_id, f.field_path,
           (unixepoch(substr(o.observed_at, 1, 19)) / 60) * 60 AS cell_start,
           json_type(o.state_value_json, f.json_path) AS kind,
           json_extract(o.state_value_json, f.json_path) AS value
    FROM observations o
    JOIN entities e ON e.id = o.entity_id
    JOIN fields f ON f.type_id = e.type_id
    WHERE o.disposition IN ('applied', 'unchanged'))
INSERT INTO observation_rollups (
    entity_id, field_path, cell_start, sample_count, null_count, invalid_count,
    sum_value, min_value, max_value)
SELECT entity_id, field_path, cell_start,
       -- json_type is NULL for an absent field, so count with CASE rather
       -- than boolean sums, which would yield NULL for an all-absent cell.
       SUM(CASE WHEN kind IN ('integer', 'real') THEN 1 ELSE 0 END),
       SUM(CASE WHEN kind IS NULL OR kind = 'null' THEN 1 ELSE 0 END),
       SUM(CASE WHEN kind IN ('integer', 'real') OR kind IS NULL OR kind = 'null' THEN 0 ELSE 1 END),
       SUM(CASE WHEN kind IN ('integer', 'real') THEN value END),
       MIN(CASE WHEN kind IN ('integer', 'real') THEN value END),
       MAX(CASE WHEN kind IN ('integer', 'real') THEN value END)
FROM samples
GROUP BY entity_id, field_path, cell_start;

-- +goose Down
CREATE INDEX observations_entity_observed_history_idx
    ON observations(entity_id, observed_at, receive_order)
    WHERE disposition IN ('applied', 'unchanged');
DROP INDEX observation_rollups_cell_start_idx;
DROP TABLE observation_rollups;
