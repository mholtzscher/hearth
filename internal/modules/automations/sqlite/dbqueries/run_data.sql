-- name: CreateRunData :exec
INSERT INTO automation_run_data (run_id, sequence, step_id, kind, recorded_at, payload_json)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListRunData :many
SELECT run_id, sequence, step_id, kind, recorded_at, payload_json
FROM automation_run_data WHERE run_id = ? ORDER BY sequence;
