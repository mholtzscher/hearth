-- name: CreateDashboard :exec
INSERT INTO dashboards (id, revision, definition_json, created_at, updated_at) VALUES (?, 1, ?, ?, ?);

-- name: GetDashboard :one
SELECT * FROM dashboards WHERE id = ?;

-- name: ListDashboards :many
SELECT * FROM dashboards WHERE id > ? ORDER BY id LIMIT ?;

-- name: ReplaceDashboard :one
UPDATE dashboards SET definition_json = ?, revision = revision + 1, updated_at = ? WHERE id = ? AND revision = ? RETURNING *;

-- name: DeleteDashboard :execrows
DELETE FROM dashboards WHERE id = ? AND revision = ?;
