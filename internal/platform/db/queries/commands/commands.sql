-- name: CreateCommand :exec
INSERT INTO commands (
    id, entity_id, adapter_id, operation, parameters_json, correlation_id,
    status, requested_at, deadline_at, accepted_at, completed_at,
    outcome_observation_id, failure_code
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetCommand :one
SELECT id, entity_id, adapter_id, operation, parameters_json, correlation_id,
       status, requested_at, deadline_at, accepted_at, completed_at,
       outcome_observation_id, failure_code
FROM commands
WHERE id = ?;

-- name: MarkCommandAccepted :execrows
UPDATE commands
SET accepted_at = COALESCE(accepted_at, ?),
    status = CASE WHEN status = 'requested' THEN 'accepted' ELSE status END
WHERE id = ? AND status IN ('requested', 'accepted', 'satisfied');

-- name: CompleteCommand :execrows
UPDATE commands
SET status = ?, completed_at = ?, failure_code = ?
WHERE id = ? AND status IN ('requested', 'accepted');

-- name: SatisfyCommandFromObservation :execrows
UPDATE commands
SET status = 'satisfied', completed_at = ?, outcome_observation_id = ?
WHERE id = ?
  AND entity_id = ?
  AND adapter_id = ?
  AND status IN ('requested', 'accepted');

-- name: InterruptActiveCommands :execrows
UPDATE commands
SET status = 'interrupted', completed_at = ?, failure_code = 'core_restarted'
WHERE status IN ('requested', 'accepted');
