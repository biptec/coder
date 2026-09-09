-- name: InsertWorkspaceMCPRequestActivity :exec
INSERT INTO workspace_mcp_request_activity (
    id,
    workspace_id,
    replica_id,
    tool,
    input,
    correlation_hash,
    status,
    started_at
) VALUES (
    @id,
    @workspace_id,
    @replica_id,
    @tool,
    @input,
    @correlation_hash,
    'running',
    @started_at
)
ON CONFLICT (id) DO NOTHING;

-- name: FinishWorkspaceMCPRequestActivity :execrows
UPDATE workspace_mcp_request_activity
SET status = @status,
    finished_at = @finished_at
WHERE id = @id
  AND workspace_id = @workspace_id
  AND status = 'running';

-- name: GetWorkspaceMCPRequestActivityByID :one
SELECT *
FROM workspace_mcp_request_activity
WHERE workspace_id = @workspace_id
  AND id = @id;

-- name: ListWorkspaceMCPRequestActivityCurrent :many
WITH latest_finished AS (
    SELECT request.id
    FROM workspace_mcp_request_activity AS request
    WHERE request.workspace_id = sqlc.arg(workspace_id)
      AND request.finished_at IS NOT NULL
    ORDER BY request.finished_at DESC, request.id DESC
    LIMIT 1
)
SELECT request.*
FROM workspace_mcp_request_activity AS request
WHERE request.workspace_id = sqlc.arg(workspace_id)
  AND (
    request.finished_at IS NULL
    OR request.id = (SELECT latest_finished.id FROM latest_finished)
  )
ORDER BY request.started_at ASC, request.id ASC;

-- name: ListWorkspaceMCPRequestActivityForRange :many
WITH previous AS (
    SELECT request.id
    FROM workspace_mcp_request_activity AS request
    WHERE request.workspace_id = sqlc.arg(workspace_id)
      AND request.finished_at IS NOT NULL
      AND request.finished_at < sqlc.arg(range_start)::timestamptz
    ORDER BY request.finished_at DESC, request.id DESC
    LIMIT 1
)
SELECT request.*
FROM workspace_mcp_request_activity AS request
WHERE request.workspace_id = sqlc.arg(workspace_id)
  AND (
    (
      request.started_at <= sqlc.arg(range_end)::timestamptz
      AND COALESCE(request.finished_at, sqlc.arg(range_end)::timestamptz) >= sqlc.arg(range_start)::timestamptz
    )
    OR request.id = (SELECT previous.id FROM previous)
  )
ORDER BY request.started_at ASC, request.id ASC;

-- name: ListWorkspaceMCPRequestActivityCandidates :many
SELECT request.*
FROM workspace_mcp_request_activity AS request
WHERE request.workspace_id = sqlc.arg(workspace_id)
  AND request.correlation_hash = sqlc.arg(correlation_hash)
  AND request.tool = ANY(sqlc.arg(tools)::text[])
  AND request.started_at <= sqlc.arg(activity_time)::timestamptz + interval '5 seconds'
  AND COALESCE(request.finished_at, sqlc.arg(activity_time)::timestamptz) >= sqlc.arg(activity_time)::timestamptz - interval '5 seconds'
ORDER BY ABS(EXTRACT(EPOCH FROM (request.started_at - sqlc.arg(activity_time)::timestamptz))) ASC, request.started_at DESC
LIMIT 32;

-- name: PruneWorkspaceMCPRequestActivity :execrows
DELETE FROM workspace_mcp_request_activity
WHERE id IN (
    SELECT completed.id
    FROM workspace_mcp_request_activity AS completed
    WHERE completed.workspace_id = @workspace_id
      AND completed.status != 'running'
    ORDER BY completed.started_at DESC, completed.id DESC
    OFFSET @history_limit
);
