CREATE TABLE mcp_trace_agent_events (
    id uuid PRIMARY KEY,
    request_id uuid NOT NULL REFERENCES mcp_trace_requests(id) ON DELETE CASCADE,
    replica_id uuid NOT NULL,
    workspace_id uuid REFERENCES workspaces(id) ON DELETE SET NULL,
    agent_id uuid NOT NULL,
    event text NOT NULL,
    details text NOT NULL DEFAULT '',
    occurred_at timestamptz NOT NULL
);

CREATE INDEX mcp_trace_agent_events_request_idx
    ON mcp_trace_agent_events (request_id, occurred_at ASC, id ASC);
CREATE INDEX mcp_trace_agent_events_agent_idx
    ON mcp_trace_agent_events (agent_id, occurred_at DESC, id DESC);
