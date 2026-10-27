-- name: CreateStepExecution :exec
INSERT INTO automation_step_executions (
    run_id, step_id, sequence, kind, phase, started_at, completed_at, evidence_json
) VALUES (
    sqlc.arg(run_id), sqlc.arg(step_id),
    (SELECT COALESCE(MAX(sequence), -1) + 1 FROM automation_step_executions WHERE run_id = sqlc.arg(run_id)),
    sqlc.arg(kind), sqlc.arg(phase), sqlc.arg(started_at), sqlc.narg(completed_at), sqlc.arg(evidence_json)
);

-- name: GetStepExecution :one
SELECT run_id, step_id, sequence, kind, phase, started_at, completed_at, evidence_json
FROM automation_step_executions WHERE run_id = ? AND step_id = ?;

-- name: ListStepExecutions :many
SELECT run_id, step_id, sequence, kind, phase, started_at, completed_at, evidence_json
FROM automation_step_executions WHERE run_id = ? ORDER BY sequence;

-- Interruption reads only unresolved effect envelopes whose counters must grow;
-- terminal payloads and legacy evidence never enter this accounting workflow.
-- name: ListRunningValueExecutions :many
SELECT run_id, step_id, sequence, kind, phase, started_at, completed_at, evidence_json
FROM automation_step_executions
WHERE phase = 'running' AND kind IN ('command', 'notification', 'function')
    AND (sqlc.arg(scope) = '' OR run_id = sqlc.arg(scope))
    AND EXISTS (SELECT 1 FROM automation_history
        WHERE id = automation_step_executions.run_id AND run_status = 'running' AND data_evidence = 'recorded');

-- name: CountRunningStepExecutions :one
SELECT count(*) FROM automation_step_executions WHERE run_id = ? AND phase = 'running';

-- Control reads never fetch prior Command, provider, Function, or Condition evidence.
-- name: ListStepExecutionControl :many
SELECT step_id, sequence, kind, phase,
    CASE WHEN kind IN ('if', 'choose') THEN json_extract(evidence_json, '$.outcome') END AS branch_outcome,
    CASE WHEN kind = 'choose' THEN json_extract(evidence_json, '$.selected_branch_id') END AS selected_branch_id
FROM automation_step_executions WHERE run_id = ? ORDER BY sequence;

-- name: GetExecutionParent :one
SELECT history.id, history.automation_id, history.kind, history.run_status, history.run_started_at,
    history.run_matched_trigger_ids_json, history.data_evidence, history.value_initialization_json,
    history.value_evidence_bytes, history.value_evidence_records, artifact.definition_json
FROM automation_history AS history
LEFT JOIN automation_definition_revisions AS artifact
    ON artifact.automation_id = history.automation_id AND artifact.revision = history.definition_revision
WHERE history.id = ?;

-- name: InitializeRunValues :execrows
UPDATE automation_history SET value_initialization_json = sqlc.arg(initialization),
    value_evidence_bytes = sqlc.arg(bytes), value_evidence_records = 1
WHERE id = sqlc.arg(run_id) AND kind = 'run' AND run_status = 'running'
    AND data_evidence = 'recorded' AND value_initialization_json IS NULL
    AND value_evidence_bytes = 0 AND value_evidence_records = 0
    AND NOT EXISTS (SELECT 1 FROM automation_step_executions WHERE run_id = automation_history.id);

-- name: AddRunValueEvidence :execrows
UPDATE automation_history SET value_evidence_bytes = value_evidence_bytes + sqlc.arg(bytes),
    value_evidence_records = value_evidence_records + sqlc.arg(records)
WHERE id = sqlc.arg(run_id) AND kind = 'run' AND run_status = 'running'
    AND value_evidence_bytes + sqlc.arg(bytes) <= sqlc.arg(byte_limit)
    AND value_evidence_records + sqlc.arg(records) <= sqlc.arg(record_limit);

-- name: CompleteStepExecution :execrows
UPDATE automation_step_executions
SET phase = ?, completed_at = ?, evidence_json = ?
WHERE run_id = ? AND step_id = ? AND phase = 'running'
    AND EXISTS (SELECT 1 FROM automation_history
        WHERE id = automation_step_executions.run_id AND kind = 'run' AND run_status = 'running');

-- name: CompleteExecutionParent :execrows
UPDATE automation_history
SET run_status = sqlc.arg(status), run_failure_code = sqlc.narg(reason), run_completed_at = sqlc.arg(completed_at)
WHERE id = sqlc.arg(run_id) AND kind = 'run' AND run_status = 'running'
    AND NOT EXISTS (SELECT 1 FROM automation_step_executions
        WHERE run_id = automation_history.id AND phase = 'running')
    AND (sqlc.arg(status) <> 'succeeded' OR NOT EXISTS (
        SELECT 1 FROM automation_step_executions
        WHERE run_id = automation_history.id AND phase IN ('failed', 'interrupted')));

-- name: InterruptStepExecutions :exec
UPDATE automation_step_executions
SET phase = 'interrupted', completed_at = sqlc.arg(completed_at),
    evidence_json = json_set(evidence_json, '$.reason_code', sqlc.arg(reason))
WHERE phase = 'running' AND (sqlc.arg(scope) = '' OR (run_id = sqlc.arg(scope)
    AND EXISTS (SELECT 1 FROM automation_history
        WHERE id = automation_step_executions.run_id AND kind = 'run' AND run_status = 'running')));
