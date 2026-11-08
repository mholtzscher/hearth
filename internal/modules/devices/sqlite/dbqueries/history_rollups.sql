-- name: UpsertObservationRollup :exec
INSERT INTO observation_rollups (
 entity_id, field_path, cell_start, sample_count, null_count, invalid_count, sum_value, min_value, max_value
) VALUES (
 sqlc.arg(entity_id), sqlc.arg(field_path), sqlc.arg(cell_start), sqlc.arg(sample_count),
 sqlc.arg(null_count), sqlc.arg(invalid_count), sqlc.arg(sum_value), sqlc.arg(min_value), sqlc.arg(max_value)
)
ON CONFLICT (entity_id, field_path, cell_start) DO UPDATE SET
 sample_count=excluded.sample_count, null_count=excluded.null_count, invalid_count=excluded.invalid_count,
 sum_value=excluded.sum_value, min_value=excluded.min_value, max_value=excluded.max_value;

-- name: ObservationRollupCell :one
SELECT * FROM observation_rollups
WHERE entity_id=sqlc.arg(entity_id) AND field_path=sqlc.arg(field_path) AND cell_start=sqlc.arg(cell_start);

-- name: HistoryEntityType :one
SELECT type_id FROM entities WHERE id = sqlc.arg(id);

-- name: HistoryRollupCells :many
SELECT * FROM observation_rollups
WHERE entity_id=sqlc.arg(entity_id) AND field_path=sqlc.arg(field_path)
 AND cell_start>=sqlc.arg(from_unix) AND cell_start<sqlc.arg(end_unix)
ORDER BY cell_start;

-- name: DeleteExpiredObservationRollups :execrows
DELETE FROM observation_rollups WHERE cell_start <= sqlc.arg(last_expired_start);
