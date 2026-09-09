ALTER TABLE workspace_command_activity
    DROP CONSTRAINT workspace_command_activity_source_check;

ALTER TABLE workspace_command_activity
    ADD CONSTRAINT workspace_command_activity_source_check
    CHECK (source IN ('agentproc', 'mcp', 'ssh', 'reconnecting_pty', 'vscode', 'jetbrains', 'chat'));

CREATE INDEX workspace_command_activity_workspace_source_started_idx
    ON workspace_command_activity (workspace_id, source, started_at DESC, id DESC);

CREATE INDEX workspace_command_activity_workspace_status_started_idx
    ON workspace_command_activity (workspace_id, status, started_at DESC, id DESC);

CREATE INDEX workspace_command_activity_workspace_tool_started_idx
    ON workspace_command_activity (workspace_id, tool, started_at DESC, id DESC);

ALTER TYPE connection_type ADD VALUE IF NOT EXISTS 'mcp';
