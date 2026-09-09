-- name: RecordWorkspaceConnectionStarted :exec
WITH active AS (
    INSERT INTO workspace_active_connections (
        workspace_id,
        agent_id,
        connection_id,
        type,
        connected_at
    ) VALUES (
        @workspace_id,
        @agent_id,
        @connection_id,
        @type,
        @connected_at
    )
    ON CONFLICT (agent_id, connection_id) DO UPDATE SET
        workspace_id = EXCLUDED.workspace_id,
        type = EXCLUDED.type,
        connected_at = LEAST(workspace_active_connections.connected_at, EXCLUDED.connected_at)
    RETURNING workspace_id, agent_id, type
)
INSERT INTO workspace_connection_activity (
    workspace_id,
    agent_id,
    type,
    last_connected_at,
    last_activity_at
)
SELECT
    workspace_id,
    agent_id,
    type,
    @connected_at,
    @connected_at
FROM active
ON CONFLICT (workspace_id, agent_id, type) DO UPDATE SET
    last_connected_at = GREATEST(workspace_connection_activity.last_connected_at, EXCLUDED.last_connected_at),
    last_activity_at = GREATEST(workspace_connection_activity.last_activity_at, EXCLUDED.last_activity_at);

-- name: RecordWorkspaceConnectionFinished :exec
WITH removed AS (
    DELETE FROM workspace_active_connections
    WHERE agent_id = @agent_id
      AND connection_id = @connection_id
    RETURNING workspace_id, agent_id, type
)
INSERT INTO workspace_connection_activity (
    workspace_id,
    agent_id,
    type,
    last_disconnected_at,
    last_activity_at
) VALUES (
    @workspace_id,
    @agent_id,
    @type,
    @disconnected_at,
    @disconnected_at
)
ON CONFLICT (workspace_id, agent_id, type) DO UPDATE SET
    last_disconnected_at = GREATEST(workspace_connection_activity.last_disconnected_at, EXCLUDED.last_disconnected_at),
    last_activity_at = GREATEST(workspace_connection_activity.last_activity_at, EXCLUDED.last_activity_at);

-- name: ResetWorkspaceActiveConnectionsByAgentID :exec
WITH removed AS (
    DELETE FROM workspace_active_connections AS active_connection
    WHERE active_connection.workspace_id = sqlc.arg(workspace_id)
      AND active_connection.agent_id = sqlc.arg(agent_id)
    RETURNING active_connection.workspace_id, active_connection.agent_id, active_connection.type
), removed_types AS (
    SELECT DISTINCT workspace_id, agent_id, type
    FROM removed
)
INSERT INTO workspace_connection_activity (
    workspace_id,
    agent_id,
    type,
    last_disconnected_at,
    last_activity_at
)
SELECT
    workspace_id,
    agent_id,
    type,
    sqlc.arg(disconnected_at),
    sqlc.arg(disconnected_at)
FROM removed_types
ON CONFLICT (workspace_id, agent_id, type) DO UPDATE SET
    last_disconnected_at = GREATEST(workspace_connection_activity.last_disconnected_at, EXCLUDED.last_disconnected_at),
    last_activity_at = GREATEST(workspace_connection_activity.last_activity_at, EXCLUDED.last_activity_at);

-- name: RecordWorkspaceConnectionActivityStarted :exec
INSERT INTO workspace_connection_activity (
    workspace_id,
    agent_id,
    type,
    last_connected_at,
    last_activity_at
) VALUES (
    @workspace_id,
    @agent_id,
    @type,
    @connected_at,
    @connected_at
)
ON CONFLICT (workspace_id, agent_id, type) DO UPDATE SET
    last_connected_at = GREATEST(workspace_connection_activity.last_connected_at, EXCLUDED.last_connected_at),
    last_activity_at = GREATEST(workspace_connection_activity.last_activity_at, EXCLUDED.last_activity_at);

-- name: RecordWorkspaceConnectionActivityFinished :exec
INSERT INTO workspace_connection_activity (
    workspace_id,
    agent_id,
    type,
    last_disconnected_at,
    last_activity_at
) VALUES (
    @workspace_id,
    @agent_id,
    @type,
    @disconnected_at,
    @disconnected_at
)
ON CONFLICT (workspace_id, agent_id, type) DO UPDATE SET
    last_disconnected_at = GREATEST(workspace_connection_activity.last_disconnected_at, EXCLUDED.last_disconnected_at),
    last_activity_at = GREATEST(workspace_connection_activity.last_activity_at, EXCLUDED.last_activity_at);

-- name: GetWorkspaceConnectionActivityByWorkspaceID :many
WITH active AS (
    SELECT
        workspace_id,
        agent_id,
        type,
        COUNT(*)::integer AS active_connections
    FROM workspace_active_connections
    WHERE workspace_id = sqlc.arg(workspace_id)
    GROUP BY workspace_id, agent_id, type
)
SELECT
    connection_activity.agent_id,
    connection_activity.type,
    connection_activity.last_connected_at,
    connection_activity.last_disconnected_at,
    connection_activity.last_activity_at,
    COALESCE(active.active_connections, 0)::integer AS active_connections
FROM workspace_connection_activity AS connection_activity
LEFT JOIN active ON
    active.workspace_id = connection_activity.workspace_id
    AND active.agent_id = connection_activity.agent_id
    AND active.type = connection_activity.type
WHERE connection_activity.workspace_id = sqlc.arg(workspace_id)
ORDER BY connection_activity.type, connection_activity.agent_id;
