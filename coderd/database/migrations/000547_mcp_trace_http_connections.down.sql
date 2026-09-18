DROP TABLE mcp_trace_http_connection_events;

DROP INDEX mcp_trace_requests_http_connection_idx;

ALTER TABLE mcp_trace_requests
DROP COLUMN http_connection_id;
