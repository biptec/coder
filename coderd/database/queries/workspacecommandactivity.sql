-- name: InsertWorkspaceCommandActivity :exec
INSERT INTO workspace_command_activity (
    id,
    workspace_id,
    agent_id,
    session_id,
    source,
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

-- name: GetWorkspaceCommandActivityByWorkspaceID :many
WITH recent_completed AS (
    SELECT completed.*
    FROM workspace_command_activity AS completed
    WHERE completed.workspace_id = sqlc.arg(workspace_id)
      AND completed.status != 'running'
    ORDER BY completed.started_at DESC, completed.id DESC
    LIMIT sqlc.arg(history_limit)
), running AS (
    SELECT active.*
    FROM workspace_command_activity AS active
    WHERE active.workspace_id = sqlc.arg(workspace_id)
      AND active.status = 'running'
)
SELECT * FROM running
UNION ALL
SELECT * FROM recent_completed
ORDER BY started_at DESC, id DESC;

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
