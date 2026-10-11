-- name: ActiveRuntime :one
SELECT r.runtime_id FROM adapter_instances i JOIN adapter_runtimes r ON r.runtime_id = i.active_runtime_id
WHERE i.adapter_id = ? AND r.adapter_id = i.adapter_id AND r.runtime_id = ? AND r.ended_at IS NULL;

-- name: UpsertDestination :exec
INSERT INTO notification_destinations(id,adapter_id,local_key,runtime_id,name) VALUES (?,?,?,?,?)
ON CONFLICT(adapter_id,local_key) DO UPDATE SET runtime_id=excluded.runtime_id,name=excluded.name;

-- name: GetDestinationByKey :one
SELECT * FROM notification_destinations WHERE adapter_id=? AND local_key=?;

-- name: GetDestination :one
SELECT d.*, CAST(CASE WHEN r.runtime_id IS NOT NULL AND r.ended_at IS NULL AND i.health_status != 'unhealthy' THEN 1 ELSE 0 END AS INTEGER) AS available
FROM notification_destinations d
LEFT JOIN adapter_instances i ON i.adapter_id=d.adapter_id AND i.active_runtime_id=d.runtime_id
LEFT JOIN adapter_runtimes r ON r.runtime_id=i.active_runtime_id AND r.adapter_id=d.adapter_id
WHERE d.id=?;

-- name: ListDestinations :many
SELECT d.*, CAST(CASE WHEN r.runtime_id IS NOT NULL AND r.ended_at IS NULL AND i.health_status != 'unhealthy' THEN 1 ELSE 0 END AS INTEGER) AS available
FROM notification_destinations d
LEFT JOIN adapter_instances i ON i.adapter_id=d.adapter_id AND i.active_runtime_id=d.runtime_id
LEFT JOIN adapter_runtimes r ON r.runtime_id=i.active_runtime_id AND r.adapter_id=d.adapter_id
WHERE d.id > ? ORDER BY d.id LIMIT ?;

-- name: InsertAttempt :exec
INSERT INTO notification_attempts(id,destination_id,destination_name,adapter_id,local_key,runtime_id,title,message,started_at,deadline) VALUES (?,?,?,?,?,?,?,?,?,?);

-- name: GetAttempt :one
SELECT * FROM notification_attempts WHERE id=?;

-- name: CompleteAttempt :execrows
UPDATE notification_attempts SET outcome=?,reason_code=?,receipt=?,completed_at=? WHERE id=? AND outcome='';

-- name: RecoverAttempts :exec
UPDATE notification_attempts SET outcome='unknown',reason_code='core_restarted',completed_at=? WHERE outcome='';

-- name: ListAttempts :many
SELECT * FROM notification_attempts
WHERE (sqlc.arg(destination_id) = '' OR destination_id=sqlc.arg(destination_id))
AND (sqlc.arg(before_time) = '' OR started_at < sqlc.arg(before_time) OR (started_at=sqlc.arg(before_time) AND id < sqlc.arg(before_id)))
ORDER BY started_at DESC,id DESC LIMIT sqlc.arg(page_limit);

-- name: DeleteHistoryBefore :execrows
DELETE FROM notification_attempts WHERE id IN (SELECT n.id FROM notification_attempts n WHERE n.outcome!='' AND n.completed_at < ? ORDER BY n.completed_at,n.id LIMIT ?);
