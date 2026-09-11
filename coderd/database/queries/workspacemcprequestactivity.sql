-- name: InsertWorkspaceMCPRequestActivity :exec
INSERT INTO workspace_mcp_request_activity (
    id,
    workspace_id,
    replica_id,
    tool,
    input,
    correlation_hash,
    status,
    started_at,
    heartbeat_at
) VALUES (
    @id,
    @workspace_id,
    @replica_id,
    @tool,
    @input,
    @correlation_hash,
    'running',
    @started_at,
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

-- name: HeartbeatWorkspaceMCPRequestActivity :execrows
UPDATE workspace_mcp_request_activity
SET heartbeat_at = sqlc.arg(heartbeat_at)::timestamptz
WHERE id = sqlc.arg(id)
  AND workspace_id = sqlc.arg(workspace_id)
  AND status = 'running';

-- name: InterruptStaleWorkspaceMCPRequestActivity :execrows
UPDATE workspace_mcp_request_activity AS request
SET status = 'interrupted',
    -- The last request heartbeat is the last point at which MCP work is known
    -- to have been active. Using cleanup time would hide at least one full
    -- stale-grace interval from the computed Idle timeline.
    finished_at = request.heartbeat_at
WHERE request.workspace_id = sqlc.arg(workspace_id)
  AND request.status = 'running'
  AND request.heartbeat_at < sqlc.arg(stale_before)::timestamptz
  AND (
    -- The current replica can safely recover its own orphaned requests because
    -- every live long-running request refreshes heartbeat_at. This also repairs
    -- a failed final DB write without waiting for coderd to restart.
    request.replica_id = sqlc.arg(current_replica_id)::uuid
    OR EXISTS (
      SELECT 1
      FROM replicas AS replica
      WHERE replica.id = request.replica_id
        AND (
          replica.stopped_at IS NOT NULL
          OR replica.updated_at < sqlc.arg(stale_before)::timestamptz
        )
    )
    OR NOT EXISTS (
      SELECT 1
      FROM replicas AS replica
      WHERE replica.id = request.replica_id
    )
  );

-- name: CountWorkspaceIdleActivity :one
WITH ordered AS (
    SELECT
        request.id,
        request.started_at,
        COALESCE(request.finished_at, 'infinity'::timestamptz) AS finished_at,
        MAX(COALESCE(request.finished_at, 'infinity'::timestamptz)) OVER (
            ORDER BY request.started_at ASC, request.id ASC
            ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
        ) AS previous_max_finished_at
    FROM workspace_mcp_request_activity AS request
    WHERE request.workspace_id = sqlc.arg(workspace_id)
), marked AS (
    SELECT
        ordered.*,
        CASE
            WHEN previous_max_finished_at IS NULL OR started_at > previous_max_finished_at THEN 1
            ELSE 0
        END AS starts_group
    FROM ordered
), grouped AS (
    SELECT
        marked.*,
        SUM(starts_group) OVER (ORDER BY started_at ASC, id ASC) AS group_id
    FROM marked
), busy AS (
    SELECT
        group_id,
        MIN(started_at) AS started_at,
        MAX(finished_at) AS finished_at
    FROM grouped
    GROUP BY group_id
), idle AS (
    SELECT
        finished_at AS started_at,
        LEAD(started_at) OVER (ORDER BY started_at ASC, group_id ASC) AS finished_at
    FROM busy
), filtered AS (
    SELECT started_at, finished_at
    FROM idle
    WHERE started_at < 'infinity'::timestamptz
      AND COALESCE(finished_at, NOW()) > started_at
      AND (
        sqlc.arg(started_after)::timestamptz = '0001-01-01 00:00:00Z'::timestamptz
        OR started_at >= sqlc.arg(started_after)::timestamptz
      )
      AND (
        sqlc.arg(started_before)::timestamptz = '0001-01-01 00:00:00Z'::timestamptz
        OR started_at <= sqlc.arg(started_before)::timestamptz
      )
      AND (
        sqlc.arg(duration_min_ms)::bigint < 0
        OR EXTRACT(EPOCH FROM (COALESCE(finished_at, NOW()) - started_at)) * 1000 >= sqlc.arg(duration_min_ms)::bigint
      )
      AND (
        sqlc.arg(duration_max_ms)::bigint < 0
        OR EXTRACT(EPOCH FROM (COALESCE(finished_at, NOW()) - started_at)) * 1000 <= sqlc.arg(duration_max_ms)::bigint
      )
)
SELECT COUNT(*)::bigint
FROM filtered;

-- name: ListWorkspaceIdleActivity :many
WITH ordered AS (
    SELECT
        request.id,
        request.started_at,
        COALESCE(request.finished_at, 'infinity'::timestamptz) AS finished_at,
        MAX(COALESCE(request.finished_at, 'infinity'::timestamptz)) OVER (
            ORDER BY request.started_at ASC, request.id ASC
            ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
        ) AS previous_max_finished_at
    FROM workspace_mcp_request_activity AS request
    WHERE request.workspace_id = sqlc.arg(workspace_id)
), marked AS (
    SELECT
        ordered.*,
        CASE
            WHEN previous_max_finished_at IS NULL OR started_at > previous_max_finished_at THEN 1
            ELSE 0
        END AS starts_group
    FROM ordered
), grouped AS (
    SELECT
        marked.*,
        SUM(starts_group) OVER (ORDER BY started_at ASC, id ASC) AS group_id
    FROM marked
), busy AS (
    SELECT
        group_id,
        MIN(started_at) AS started_at,
        MAX(finished_at) AS finished_at
    FROM grouped
    GROUP BY group_id
), idle AS (
    SELECT
        finished_at AS started_at,
        LEAD(started_at) OVER (ORDER BY started_at ASC, group_id ASC) AS finished_at
    FROM busy
), filtered AS (
    SELECT started_at, finished_at
    FROM idle
    WHERE started_at < 'infinity'::timestamptz
      AND COALESCE(finished_at, NOW()) > started_at
      AND (
        sqlc.arg(started_after)::timestamptz = '0001-01-01 00:00:00Z'::timestamptz
        OR started_at >= sqlc.arg(started_after)::timestamptz
      )
      AND (
        sqlc.arg(started_before)::timestamptz = '0001-01-01 00:00:00Z'::timestamptz
        OR started_at <= sqlc.arg(started_before)::timestamptz
      )
      AND (
        sqlc.arg(duration_min_ms)::bigint < 0
        OR EXTRACT(EPOCH FROM (COALESCE(finished_at, NOW()) - started_at)) * 1000 >= sqlc.arg(duration_min_ms)::bigint
      )
      AND (
        sqlc.arg(duration_max_ms)::bigint < 0
        OR EXTRACT(EPOCH FROM (COALESCE(finished_at, NOW()) - started_at)) * 1000 <= sqlc.arg(duration_max_ms)::bigint
      )
)
SELECT
    started_at::timestamptz AS started_at,
    COALESCE(finished_at, NOW())::timestamptz AS finished_at,
    (finished_at IS NULL)::boolean AS is_current
FROM filtered
ORDER BY
    is_current DESC,
    CASE WHEN sqlc.arg(sort_direction)::text = 'asc' THEN started_at END ASC,
    CASE WHEN sqlc.arg(sort_direction)::text = 'desc' THEN started_at END DESC,
    started_at DESC
LIMIT sqlc.arg(page_limit)
OFFSET sqlc.arg(page_offset);

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
