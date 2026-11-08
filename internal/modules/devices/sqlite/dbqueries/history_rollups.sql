-- name: UpsertObservationRollup :exec
-- A sample adds one to exactly one of the three counts; value is NULL unless
-- the sample is numeric, so sums and extrema only ever see numbers.
INSERT INTO observation_rollups (
    entity_id, field_path, cell_start, sample_count, null_count, invalid_count,
    sum_value, min_value, max_value
) VALUES (
    sqlc.arg(entity_id), sqlc.arg(field_path), sqlc.arg(cell_start),
    sqlc.arg(sample_count), sqlc.arg(null_count), sqlc.arg(invalid_count),
    sqlc.arg(value), sqlc.arg(value), sqlc.arg(value)
)
ON CONFLICT (entity_id, field_path, cell_start) DO UPDATE SET
    sample_count = observation_rollups.sample_count + excluded.sample_count,
    null_count = observation_rollups.null_count + excluded.null_count,
    invalid_count = observation_rollups.invalid_count + excluded.invalid_count,
    sum_value = CASE
        WHEN excluded.sum_value IS NULL THEN observation_rollups.sum_value
        WHEN observation_rollups.sum_value IS NULL THEN excluded.sum_value
        ELSE observation_rollups.sum_value + excluded.sum_value
    END,
    min_value = CASE
        WHEN excluded.min_value IS NOT NULL
            AND (observation_rollups.min_value IS NULL OR excluded.min_value < observation_rollups.min_value)
        THEN excluded.min_value ELSE observation_rollups.min_value
    END,
    max_value = CASE
        WHEN excluded.max_value IS NOT NULL
            AND (observation_rollups.max_value IS NULL OR excluded.max_value > observation_rollups.max_value)
        THEN excluded.max_value ELSE observation_rollups.max_value
    END;

-- name: HistoryEntityType :one
SELECT type_id FROM entities WHERE id = sqlc.arg(id);

-- name: HistoryRollupBuckets :many
-- Groups one field's cells into fixed-width buckets from from_unix. Sums keep
-- INTEGER while every cell sum is an integer; extrema compare numerically.
SELECT CAST((cell_start - sqlc.arg(from_unix)) / sqlc.arg(bucket_seconds) AS INTEGER) AS bucket_index,
       CAST(SUM(sample_count) AS INTEGER) AS sample_count,
       CAST(SUM(null_count) AS INTEGER) AS null_count,
       CAST(SUM(invalid_count) AS INTEGER) AS invalid_count,
       -- CASE keeps the column untyped in Go, so integer sums stay int64.
       CASE WHEN SUM(sample_count) > 0 THEN SUM(sum_value) END AS sum_value,
       MIN(min_value) AS min_value,
       MAX(max_value) AS max_value
FROM observation_rollups
WHERE entity_id = sqlc.arg(entity_id) AND field_path = sqlc.arg(field_path)
  AND cell_start >= sqlc.arg(from_unix) AND cell_start < sqlc.arg(end_unix)
GROUP BY bucket_index
ORDER BY bucket_index;

-- name: DeleteExpiredObservationRollups :execrows
-- Compare starts to a precomputed boundary so the retention index is usable.
DELETE FROM observation_rollups WHERE cell_start <= sqlc.arg(last_expired_start);
