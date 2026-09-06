-- name: AcquireWorkspaceVolumeCopyLifecycleLock :one
SELECT id
FROM workspaces
WHERE id = @workspace_id
FOR UPDATE;

-- name: GetWorkspaceVolumeCopyLockByWorkspaceID :one
SELECT *
FROM workspace_volume_copy_locks
WHERE workspace_id = @workspace_id;

-- name: InsertWorkspaceVolumeCopyLock :exec
INSERT INTO workspace_volume_copy_locks (
    workspace_id,
    operation_id,
    created_at
) VALUES (
    @workspace_id,
    @operation_id,
    @created_at
);

-- name: DeleteWorkspaceVolumeCopyLocksByOperationID :exec
DELETE FROM workspace_volume_copy_locks
WHERE operation_id = @operation_id;

-- name: InsertWorkspaceVolumeCopyOperation :one
INSERT INTO workspace_volume_copy_operations (
    id,
    created_at,
    updated_at,
    initiator_id,
    source_workspace_id,
    destination_workspace_id,
    allow_source_running,
    volumes,
    status,
    namespace,
    job_name,
    error,
    sync_of
) VALUES (
    @id,
    @created_at,
    @updated_at,
    @initiator_id,
    @source_workspace_id,
    @destination_workspace_id,
    @allow_source_running,
    @volumes,
    @status,
    @namespace,
    @job_name,
    @error,
    @sync_of
)
RETURNING *;

-- name: GetWorkspaceVolumeCopyOperationByID :one
SELECT *
FROM workspace_volume_copy_operations
WHERE id = @id;

-- name: GetWorkspaceVolumeCopyOperationsByWorkspaceID :many
SELECT *
FROM workspace_volume_copy_operations
WHERE source_workspace_id = @workspace_id
   OR destination_workspace_id = @workspace_id
ORDER BY created_at DESC
LIMIT @limit_count;

-- name: GetActiveWorkspaceVolumeCopyOperations :many
SELECT *
FROM workspace_volume_copy_operations
WHERE status IN ('pending', 'running')
ORDER BY created_at ASC;

-- name: MarkWorkspaceVolumeCopyOperationRunning :one
UPDATE workspace_volume_copy_operations
SET status = 'running',
    updated_at = @updated_at,
    started_at = COALESCE(started_at, @started_at)
WHERE id = @id
  AND status IN ('pending', 'running')
RETURNING *;

-- name: MarkWorkspaceVolumeCopyOperationSucceeded :one
UPDATE workspace_volume_copy_operations
SET status = 'succeeded',
    updated_at = @updated_at,
    completed_at = @completed_at,
    error = ''
WHERE id = @id
  AND status IN ('pending', 'running')
RETURNING *;

-- name: MarkWorkspaceVolumeCopyOperationFailed :one
UPDATE workspace_volume_copy_operations
SET status = 'failed',
    updated_at = @updated_at,
    completed_at = @completed_at,
    error = @error
WHERE id = @id
  AND status IN ('pending', 'running')
RETURNING *;
