-- name: GetTemperatureSeries :many
-- Entity identity and aggregates share one snapshot. Only canonical, in-schema
-- integers enter the numeric aggregate; invalid retained values are counted.
WITH samples AS (
 SELECT (unixepoch(substr(observed_at, 1, 19) || 'Z') - CAST(sqlc.arg(start_unix) AS INTEGER)) / CAST(sqlc.arg(bucket_seconds) AS INTEGER) AS bucket,
        CASE WHEN state_value_json = CAST(CAST(state_value_json AS INTEGER) AS TEXT)
             AND CAST(state_value_json AS INTEGER) BETWEEN -273150 AND 1000000
             THEN CAST(state_value_json AS INTEGER) END AS number
 FROM observations
 WHERE entity_id = sqlc.arg(entity_id) AND observed_at >= sqlc.arg(from_time) AND observed_at < sqlc.arg(to_time)
   AND disposition IN ('applied', 'unchanged')
   AND EXISTS (SELECT 1 FROM entities WHERE id = sqlc.arg(entity_id) AND type_id = 'hearth.temperature/v1')
), grouped AS (
 SELECT bucket, count(*) AS count, count(*) - count(number) AS invalid,
        min(number) AS minimum, max(number) AS maximum, avg(number) AS mean
 FROM samples GROUP BY bucket
)
SELECT e.type_id, g.bucket, g.count, g.invalid, g.minimum, g.maximum, g.mean
FROM entities e LEFT JOIN grouped g ON true WHERE e.id = sqlc.arg(entity_id) ORDER BY g.bucket;
