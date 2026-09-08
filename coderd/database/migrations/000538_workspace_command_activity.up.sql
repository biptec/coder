CREATE TABLE workspace_command_activity (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    agent_id uuid NOT NULL,
    session_id uuid NOT NULL,
    source text NOT NULL CHECK (source IN ('agentproc', 'ssh')),
    command text NOT NULL DEFAULT '',
    argv text[] NOT NULL DEFAULT '{}',
    work_dir text NOT NULL DEFAULT '',
    status text NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'interrupted')),
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    exit_code integer
);

CREATE INDEX workspace_command_activity_workspace_started_idx
    ON workspace_command_activity (workspace_id, started_at DESC, id DESC);

CREATE INDEX workspace_command_activity_agent_running_idx
    ON workspace_command_activity (agent_id, session_id)
    WHERE status = 'running';

CREATE TABLE workspace_connection_activity (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    agent_id uuid NOT NULL REFERENCES workspace_agents(id) ON DELETE CASCADE,
    type connection_type NOT NULL,
    last_connected_at timestamptz,
    last_disconnected_at timestamptz,
    last_activity_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, agent_id, type)
);

CREATE TABLE workspace_active_connections (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    agent_id uuid NOT NULL REFERENCES workspace_agents(id) ON DELETE CASCADE,
    connection_id uuid NOT NULL,
    type connection_type NOT NULL,
    connected_at timestamptz NOT NULL,
    PRIMARY KEY (agent_id, connection_id)
);

CREATE INDEX workspace_active_connections_workspace_idx
    ON workspace_active_connections (workspace_id, type, connected_at);
