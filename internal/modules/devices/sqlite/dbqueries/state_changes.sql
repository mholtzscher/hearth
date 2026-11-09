-- name: InsertStateChange :exec
INSERT INTO state_changes (entity_id, field_path, observed_at, receive_order, value_json)
VALUES (sqlc.arg(entity_id), sqlc.arg(field_path), sqlc.arg(observed_at), sqlc.arg(receive_order), sqlc.arg(value_json));

-- name: StateChangeSeed :one
SELECT observed_at, receive_order, value_json FROM state_changes
WHERE entity_id=sqlc.arg(entity_id) AND field_path=sqlc.arg(field_path) AND observed_at<sqlc.arg(range_from)
ORDER BY observed_at DESC, receive_order DESC
LIMIT 1;

-- name: StateChangesInRange :many
SELECT observed_at, receive_order, value_json FROM state_changes
WHERE entity_id=sqlc.arg(entity_id) AND field_path=sqlc.arg(field_path)
 AND observed_at>=sqlc.arg(range_from) AND observed_at<sqlc.arg(range_to)
ORDER BY observed_at, receive_order
LIMIT sqlc.arg(row_limit);

-- name: DeleteExpiredStateChanges :execrows
-- Keeps the newest change at or before the cutoff for each field, so a
-- timeline that starts after the cutoff still knows its opening value.
DELETE FROM state_changes AS c
WHERE c.observed_at < sqlc.arg(cutoff)
 AND EXISTS (
  SELECT 1 FROM state_changes AS n
  WHERE n.entity_id=c.entity_id AND n.field_path=c.field_path
   AND (n.observed_at, n.receive_order) > (c.observed_at, c.receive_order)
   AND n.observed_at <= sqlc.arg(cutoff));
