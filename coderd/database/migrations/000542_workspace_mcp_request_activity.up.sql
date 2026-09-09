CREATE TABLE workspace_mcp_request_activity (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    replica_id uuid NOT NULL,
    tool text NOT NULL,
    input text NOT NULL DEFAULT '',
    correlation_hash text NOT NULL DEFAULT '',
    status text NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'interrupted')),
    started_at timestamptz NOT NULL,
    finished_at timestamptz
);

CREATE INDEX workspace_mcp_request_activity_workspace_started_idx
    ON workspace_mcp_request_activity (workspace_id, started_at DESC, id DESC);

CREATE INDEX workspace_mcp_request_activity_workspace_running_idx
    ON workspace_mcp_request_activity (workspace_id, started_at DESC)
    WHERE finished_at IS NULL;

CREATE INDEX workspace_mcp_request_activity_workspace_tool_started_idx
    ON workspace_mcp_request_activity (workspace_id, tool, started_at DESC);

CREATE INDEX workspace_mcp_request_activity_workspace_correlation_started_idx
    ON workspace_mcp_request_activity (workspace_id, correlation_hash, started_at DESC)
    WHERE correlation_hash != '';
