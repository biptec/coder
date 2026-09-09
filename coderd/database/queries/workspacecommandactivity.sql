-- name: InsertWorkspaceCommandActivity :exec
INSERT INTO workspace_command_activity (
    id,
    workspace_id,
    agent_id,
    session_id,
    source,
    tool,
    command,
    argv,
    work_dir,
    status,
    started_at
) VALUES (
    @id,
    @workspace_id,
    @agent_id,
    @session_id,
    @source,
    @tool,
    @command,
    @argv,
    @work_dir,
    'running',
    @started_at
)
ON CONFLICT (id) DO NOTHING;

-- name: FinishWorkspaceCommandActivity :execrows
UPDATE workspace_command_activity
SET status = CASE WHEN @exit_code::integer = 0 THEN 'succeeded' ELSE 'failed' END,
    finished_at = @finished_at,
    exit_code = @exit_code
WHERE id = @id
  AND workspace_id = @workspace_id
  AND agent_id = @agent_id
  AND session_id = @session_id
  AND status = 'running';

-- name: InterruptWorkspaceCommandActivityByAgentSession :execrows
UPDATE workspace_command_activity
SET status = 'interrupted',
    finished_at = @finished_at,
    exit_code = NULL
WHERE workspace_id = @workspace_id
  AND agent_id = @agent_id
  AND session_id != @session_id
  AND status = 'running';

-- name: ListWorkspaceCommandActivity :many
SELECT *
FROM workspace_command_activity
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (
    COALESCE(array_length(sqlc.arg(statuses)::text[], 1), 0) = 0
    OR status = ANY(sqlc.arg(statuses)::text[])
  )
  AND (
    COALESCE(array_length(sqlc.arg(tools)::text[], 1), 0) = 0
    OR tool = ANY(sqlc.arg(tools)::text[])
  )
  AND (
    COALESCE(array_length(sqlc.arg(sources)::text[], 1), 0) = 0
    OR source = ANY(sqlc.arg(sources)::text[])
  )
  AND (
    sqlc.arg(search)::text = ''
    OR strpos(lower(command), lower(sqlc.arg(search)::text)) > 0
    OR strpos(lower(array_to_string(argv, ' ')), lower(sqlc.arg(search)::text)) > 0
  )
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
ORDER BY
  CASE WHEN sqlc.arg(sort_by)::text = 'id' AND sqlc.arg(sort_direction)::text = 'asc' THEN id END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'id' AND sqlc.arg(sort_direction)::text = 'desc' THEN id END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'status' AND sqlc.arg(sort_direction)::text = 'asc' THEN status END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'status' AND sqlc.arg(sort_direction)::text = 'desc' THEN status END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'started' AND sqlc.arg(sort_direction)::text = 'asc' THEN started_at END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'started' AND sqlc.arg(sort_direction)::text = 'desc' THEN started_at END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'duration' AND sqlc.arg(sort_direction)::text = 'asc' THEN COALESCE(finished_at, NOW()) - started_at END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'duration' AND sqlc.arg(sort_direction)::text = 'desc' THEN COALESCE(finished_at, NOW()) - started_at END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'tool' AND sqlc.arg(sort_direction)::text = 'asc' THEN tool END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'tool' AND sqlc.arg(sort_direction)::text = 'desc' THEN tool END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'source' AND sqlc.arg(sort_direction)::text = 'asc' THEN source END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'source' AND sqlc.arg(sort_direction)::text = 'desc' THEN source END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'command' AND sqlc.arg(sort_direction)::text = 'asc' THEN COALESCE(NULLIF(command, ''), array_to_string(argv, ' ')) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'command' AND sqlc.arg(sort_direction)::text = 'desc' THEN COALESCE(NULLIF(command, ''), array_to_string(argv, ' ')) END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'exit' AND sqlc.arg(sort_direction)::text = 'asc' THEN exit_code END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'exit' AND sqlc.arg(sort_direction)::text = 'desc' THEN exit_code END DESC,
  started_at DESC,
  id DESC
LIMIT sqlc.arg(page_limit)
OFFSET sqlc.arg(page_offset);

-- name: CountWorkspaceCommandActivity :one
SELECT COUNT(*)::bigint
FROM workspace_command_activity
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (
    COALESCE(array_length(sqlc.arg(statuses)::text[], 1), 0) = 0
    OR status = ANY(sqlc.arg(statuses)::text[])
  )
  AND (
    COALESCE(array_length(sqlc.arg(tools)::text[], 1), 0) = 0
    OR tool = ANY(sqlc.arg(tools)::text[])
  )
  AND (
    COALESCE(array_length(sqlc.arg(sources)::text[], 1), 0) = 0
    OR source = ANY(sqlc.arg(sources)::text[])
  )
  AND (
    sqlc.arg(search)::text = ''
    OR strpos(lower(command), lower(sqlc.arg(search)::text)) > 0
    OR strpos(lower(array_to_string(argv, ' ')), lower(sqlc.arg(search)::text)) > 0
  )
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
  );

-- name: DeleteWorkspaceCommandActivityByFilter :execrows
DELETE FROM workspace_command_activity
WHERE workspace_id = sqlc.arg(workspace_id)
  AND (
    COALESCE(array_length(sqlc.arg(statuses)::text[], 1), 0) = 0
    OR status = ANY(sqlc.arg(statuses)::text[])
  )
  AND (
    COALESCE(array_length(sqlc.arg(tools)::text[], 1), 0) = 0
    OR tool = ANY(sqlc.arg(tools)::text[])
  )
  AND (
    COALESCE(array_length(sqlc.arg(sources)::text[], 1), 0) = 0
    OR source = ANY(sqlc.arg(sources)::text[])
  )
  AND (
    sqlc.arg(search)::text = ''
    OR strpos(lower(command), lower(sqlc.arg(search)::text)) > 0
    OR strpos(lower(array_to_string(argv, ' ')), lower(sqlc.arg(search)::text)) > 0
  )
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
  );

-- name: DeleteWorkspaceCommandActivityByIDs :execrows
DELETE FROM workspace_command_activity
WHERE workspace_id = sqlc.arg(workspace_id)
  AND id = ANY(sqlc.arg(ids)::uuid[]);

-- name: PruneWorkspaceCommandActivity :execrows
DELETE FROM workspace_command_activity
WHERE id IN (
    SELECT completed.id
    FROM workspace_command_activity AS completed
    WHERE completed.workspace_id = sqlc.arg(workspace_id)
      AND completed.status != 'running'
    ORDER BY completed.started_at DESC, completed.id DESC
    OFFSET sqlc.arg(history_limit)
);
