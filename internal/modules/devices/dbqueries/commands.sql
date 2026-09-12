-- name: CreateCommand :exec
INSERT INTO commands (
    id, entity_id, adapter_id, runtime_id, operation, parameters_json,
    correlation_id, status, requested_at, deadline_at, accepted_at,
    completed_at, outcome_observation_id, failure_code
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetCommand :one
SELECT id, entity_id, adapter_id, runtime_id, operation, parameters_json,
       correlation_id, status, requested_at, deadline_at, accepted_at,
       completed_at, outcome_observation_id, failure_code
FROM commands
WHERE id = ?;

-- name: ListEntityCommandsFirstPage :many
SELECT id, entity_id, adapter_id, runtime_id, operation, parameters_json,
       correlation_id, status, requested_at, deadline_at, accepted_at,
       completed_at, outcome_observation_id, failure_code
FROM commands
WHERE entity_id = ?
ORDER BY requested_at DESC, id DESC
LIMIT ?;

-- name: ListEntityCommandsAfter :many
SELECT id, entity_id, adapter_id, runtime_id, operation, parameters_json,
       correlation_id, status, requested_at, deadline_at, accepted_at,
       completed_at, outcome_observation_id, failure_code
FROM commands
WHERE entity_id = ?
  AND (requested_at < ? OR (requested_at = ? AND id < ?))
ORDER BY requested_at DESC, id DESC
LIMIT ?;

-- MarkCommandAccepted commits one acceptance write and returns the committed
-- row. Callers read the prior row in the same transaction to classify the
-- transition: only requested -> accepted is a real transition.
-- name: MarkCommandAccepted :one
UPDATE commands
SET accepted_at = COALESCE(accepted_at, ?),
    status = CASE WHEN status = 'requested' THEN 'accepted' ELSE status END
WHERE id = ? AND status IN ('requested', 'accepted', 'satisfied')
RETURNING id, entity_id, adapter_id, runtime_id, operation, parameters_json,
          correlation_id, status, requested_at, deadline_at, accepted_at,
          completed_at, outcome_observation_id, failure_code;

-- name: CompleteCommand :one
UPDATE commands
SET status = ?, completed_at = ?, failure_code = ?
WHERE id = ? AND status IN ('requested', 'accepted')
RETURNING id, entity_id, adapter_id, runtime_id, operation, parameters_json,
          correlation_id, status, requested_at, deadline_at, accepted_at,
          completed_at, outcome_observation_id, failure_code;

-- name: SatisfyCommandFromObservation :one
UPDATE commands
SET status = 'satisfied', completed_at = ?, outcome_observation_id = ?
WHERE id = ?
  AND entity_id = ?
  AND adapter_id = ?
  AND runtime_id = ?
  AND status IN ('requested', 'accepted')
RETURNING id, entity_id, adapter_id, runtime_id, operation, parameters_json,
          correlation_id, status, requested_at, deadline_at, accepted_at,
          completed_at, outcome_observation_id, failure_code;

-- InterruptActiveCommands commits one interruption of every active row and
-- returns each post-transition record. Repeating the call matches no active
-- row and returns an empty slice.
-- name: InterruptActiveCommands :many
UPDATE commands
SET status = 'interrupted', completed_at = ?, failure_code = 'core_restarted'
WHERE status IN ('requested', 'accepted')
RETURNING id, entity_id, adapter_id, runtime_id, operation, parameters_json,
          correlation_id, status, requested_at, deadline_at, accepted_at,
          completed_at, outcome_observation_id, failure_code;
