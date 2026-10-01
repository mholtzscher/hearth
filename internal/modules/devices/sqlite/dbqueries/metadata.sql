-- name: UpdateDeviceNameOverride :exec
UPDATE devices SET name_override = sqlc.narg(name_override), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: UpdateEntityNameOverride :exec
UPDATE entities SET name_override = sqlc.narg(name_override), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);
