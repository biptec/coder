ALTER TABLE mcp_trace_requests
ADD COLUMN http_connection_id uuid;

CREATE INDEX mcp_trace_requests_http_connection_idx
    ON mcp_trace_requests (http_connection_id, received_at ASC, id ASC)
    WHERE http_connection_id IS NOT NULL;

CREATE TABLE mcp_trace_http_connection_events (
    id uuid PRIMARY KEY,
    connection_id uuid NOT NULL,
    replica_id uuid NOT NULL,
    state text NOT NULL,
    occurred_at timestamptz NOT NULL
);

CREATE INDEX mcp_trace_http_connection_events_connection_idx
    ON mcp_trace_http_connection_events (connection_id, occurred_at ASC, id ASC);

CREATE INDEX mcp_trace_http_connection_events_occurred_idx
    ON mcp_trace_http_connection_events (occurred_at ASC, id ASC);
