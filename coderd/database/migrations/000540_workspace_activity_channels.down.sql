DELETE FROM workspace_connection_activity WHERE type::text = 'mcp';

DROP INDEX workspace_command_activity_workspace_tool_started_idx;
DROP INDEX workspace_command_activity_workspace_status_started_idx;
DROP INDEX workspace_command_activity_workspace_source_started_idx;

ALTER TABLE workspace_command_activity
    DROP CONSTRAINT workspace_command_activity_source_check;

-- Preserve history when rolling back to the old two-source model. MCP and
-- Coder Chat used the agent process execution path before this migration;
-- terminal/IDE channels used the SSH/session path.
UPDATE workspace_command_activity
SET source = CASE
    WHEN source IN ('mcp', 'chat') THEN 'agentproc'
    WHEN source IN ('reconnecting_pty', 'vscode', 'jetbrains') THEN 'ssh'
    ELSE source
END
WHERE source NOT IN ('agentproc', 'ssh');

ALTER TABLE workspace_command_activity
    ADD CONSTRAINT workspace_command_activity_source_check
    CHECK (source IN ('agentproc', 'ssh'));

-- No-op for ALTER TYPE connection_type ADD VALUE: Postgres does not allow
-- removing enum values safely.
