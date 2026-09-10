CREATE TABLE mcp_trace_requests (
    id uuid PRIMARY KEY,
    replica_id uuid NOT NULL,
    coder_request_id uuid,
    user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    session_id text NOT NULL DEFAULT '',
    http_method text NOT NULL,
    http_protocol text NOT NULL,
    mcp_method text NOT NULL DEFAULT '',
    jsonrpc_id text NOT NULL DEFAULT '',
    tool text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'running',
    last_stage text NOT NULL DEFAULT 'http_received',
    error_kind text NOT NULL DEFAULT '',
    http_status integer,
    response_bytes bigint NOT NULL DEFAULT 0,
    response_write_count bigint NOT NULL DEFAULT 0,
    received_at timestamptz NOT NULL,
    authenticated_at timestamptz,
    transport_entered_at timestamptz,
    parsed_at timestamptz,
    dispatched_at timestamptz,
    handler_started_at timestamptz,
    handler_finished_at timestamptz,
    mcp_finished_at timestamptz,
    response_started_at timestamptz,
    last_response_write_at timestamptz,
    canceled_at timestamptz,
    session_registered_at timestamptz,
    session_unregistered_at timestamptz,
    finished_at timestamptz
);

CREATE INDEX mcp_trace_requests_received_idx
    ON mcp_trace_requests (received_at DESC, id DESC);
CREATE INDEX mcp_trace_requests_coder_request_idx
    ON mcp_trace_requests (coder_request_id);
CREATE INDEX mcp_trace_requests_session_received_idx
    ON mcp_trace_requests (session_id, received_at DESC)
    WHERE session_id != '';
CREATE INDEX mcp_trace_requests_running_idx
    ON mcp_trace_requests (received_at DESC)
    WHERE finished_at IS NULL;

CREATE TABLE mcp_trace_connections (
    id uuid PRIMARY KEY,
    request_id uuid NOT NULL UNIQUE REFERENCES mcp_trace_requests(id) ON DELETE CASCADE,
    replica_id uuid NOT NULL,
    user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    session_id text NOT NULL DEFAULT '',
    http_protocol text NOT NULL,
    status text NOT NULL DEFAULT 'open',
    opened_at timestamptz NOT NULL,
    closed_at timestamptz,
    close_reason text NOT NULL DEFAULT ''
);

CREATE INDEX mcp_trace_connections_opened_idx
    ON mcp_trace_connections (opened_at DESC, id DESC);
CREATE INDEX mcp_trace_connections_session_opened_idx
    ON mcp_trace_connections (session_id, opened_at DESC)
    WHERE session_id != '';
CREATE INDEX mcp_trace_connections_open_idx
    ON mcp_trace_connections (opened_at DESC)
    WHERE closed_at IS NULL;
