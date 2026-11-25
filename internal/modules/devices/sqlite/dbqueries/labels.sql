-- name: CountLabels :one
SELECT COUNT(*) FROM labels;

-- name: InsertLabel :exec
INSERT INTO labels (id, name, name_key, kind, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(name_key), sqlc.arg(kind), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: GetLabel :one
SELECT id, name, name_key, kind, created_at, updated_at
FROM labels
WHERE id = sqlc.arg(id);

-- name: GetLabelIDByNameKey :one
SELECT id FROM labels WHERE name_key = sqlc.arg(name_key);

-- name: UpdateLabelName :exec
UPDATE labels
SET name = sqlc.arg(name), name_key = sqlc.arg(name_key), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: DeleteLabel :execrows
DELETE FROM labels WHERE id = sqlc.arg(id);

-- name: ListLabels :many
SELECT id, name, name_key, kind, created_at, updated_at
FROM labels
ORDER BY kind ASC, name_key ASC, id ASC;

-- name: GetLabelKinds :many
SELECT id, kind FROM labels WHERE id IN (sqlc.slice(ids));

-- name: GetDeviceAreaID :one
SELECT area_id FROM devices WHERE id = sqlc.arg(id);

-- name: GetEntityAreaID :one
SELECT area_id FROM entities WHERE id = sqlc.arg(id);

-- name: UpdateDeviceArea :exec
UPDATE devices SET area_id = sqlc.narg(area_id), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: UpdateEntityArea :exec
UPDATE entities SET area_id = sqlc.narg(area_id), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: TouchDevice :exec
UPDATE devices SET updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id);

-- name: TouchEntity :exec
UPDATE entities SET updated_at = sqlc.arg(updated_at) WHERE id = sqlc.arg(id);

-- name: ListDeviceTagIDs :many
SELECT label_id FROM device_tags WHERE device_id = sqlc.arg(device_id) ORDER BY label_id;

-- name: ListEntityTagIDs :many
SELECT label_id FROM entity_tags WHERE entity_id = sqlc.arg(entity_id) ORDER BY label_id;

-- name: DeleteDeviceTags :exec
DELETE FROM device_tags WHERE device_id = sqlc.arg(device_id);

-- name: DeleteEntityTags :exec
DELETE FROM entity_tags WHERE entity_id = sqlc.arg(entity_id);

-- name: InsertDeviceTag :exec
INSERT INTO device_tags (device_id, label_id) VALUES (sqlc.arg(device_id), sqlc.arg(label_id));

-- name: InsertEntityTag :exec
INSERT INTO entity_tags (entity_id, label_id) VALUES (sqlc.arg(entity_id), sqlc.arg(label_id));

-- name: ListDeviceAreas :many
SELECT d.id AS device_id, l.id AS label_id, l.name
FROM devices AS d
JOIN labels AS l ON l.id = d.area_id
WHERE d.id IN (sqlc.slice(device_ids));

-- name: ListDeviceTags :many
SELECT dt.device_id, l.id AS label_id, l.name
FROM device_tags AS dt
JOIN labels AS l ON l.id = dt.label_id
WHERE dt.device_id IN (sqlc.slice(device_ids))
ORDER BY dt.device_id ASC, l.name_key ASC, l.id ASC;

-- name: ListEffectiveEntityLabels :many
SELECT el.entity_id, el.label_id, el.kind, el.source, l.name
FROM entity_effective_labels AS el
JOIN labels AS l ON l.id = el.label_id
WHERE el.entity_id IN (sqlc.slice(entity_ids))
ORDER BY el.entity_id ASC, el.kind ASC, l.name_key ASC, el.label_id ASC, el.source DESC;

-- ListEntitiesBySelector is hand-written in entity_selector.go: sqlc numbers
-- repeated or named arguments, which misbinds the positional parameters its
-- slice expansion produces, and it cannot parse json_each.
