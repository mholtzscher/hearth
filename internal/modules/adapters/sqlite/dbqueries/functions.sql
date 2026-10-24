-- name: GetFunctionCatalog :one
SELECT adapter_id, catalog_revision, runtime_id, registered_at, functions_json
FROM adapter_function_catalogs WHERE adapter_id = ?;

-- name: GetFunctionCatalogOwner :one
SELECT adapter_id, runtime_id FROM adapter_function_catalogs WHERE catalog_revision = ?;

-- name: UpsertFunctionCatalog :exec
INSERT INTO adapter_function_catalogs(adapter_id,catalog_revision,runtime_id,registered_at,functions_json)
VALUES (?,?,?,?,?)
ON CONFLICT(adapter_id) DO UPDATE SET catalog_revision=excluded.catalog_revision,
    runtime_id=excluded.runtime_id, registered_at=excluded.registered_at, functions_json=excluded.functions_json;

-- name: FunctionRuntimeActive :one
SELECT r.runtime_id FROM adapter_instances i JOIN adapter_runtimes r ON r.runtime_id=i.active_runtime_id
WHERE i.adapter_id=? AND r.adapter_id=i.adapter_id AND r.runtime_id=? AND r.ended_at IS NULL;

-- name: InsertFunctionInvocation :exec
INSERT INTO adapter_function_invocations(id,adapter_id,runtime_id,catalog_revision,function_key,
    descriptor_json,arguments_json,started_at,deadline,status)
VALUES (?,?,?,?,?,?,?,?,?,'running');

-- name: GetFunctionInvocation :one
SELECT id,adapter_id,runtime_id,catalog_revision,function_key,descriptor_json,arguments_json,
    started_at,deadline,status,result_json,reason_code,completed_at
FROM adapter_function_invocations WHERE id=?;

-- name: CompleteFunctionInvocation :execrows
UPDATE adapter_function_invocations SET status=?,result_json=?,reason_code=?,completed_at=?
WHERE id=? AND status='running';

-- name: RecoverFunctionInvocations :exec
UPDATE adapter_function_invocations SET status='unknown',reason_code='core_restarted',completed_at=?
WHERE status='running';

-- name: ListFunctionInvocations :many
SELECT id,adapter_id,runtime_id,catalog_revision,function_key,descriptor_json,arguments_json,
    started_at,deadline,status,result_json,reason_code,completed_at
FROM adapter_function_invocations
WHERE adapter_id=sqlc.arg(adapter_id)
AND (sqlc.arg(function_key)='' OR function_key=sqlc.arg(function_key))
AND (sqlc.arg(before_time)='' OR started_at<sqlc.arg(before_time)
    OR (started_at=sqlc.arg(before_time) AND id<sqlc.arg(before_id)))
ORDER BY started_at DESC,id DESC LIMIT sqlc.arg(page_limit);

-- name: DeleteFunctionHistoryBefore :execrows
DELETE FROM adapter_function_invocations WHERE id IN (
    SELECT history.id FROM adapter_function_invocations AS history
    WHERE history.status!='running' AND history.completed_at < ? ORDER BY history.completed_at,history.id LIMIT ?
);
