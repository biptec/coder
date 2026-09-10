-- name: InsertMCPTraceRequest :exec
INSERT INTO mcp_trace_requests (
    id, replica_id, coder_request_id, user_id, session_id,
    http_method, http_protocol, received_at
) VALUES (
    @id, @replica_id, @coder_request_id, @user_id, @session_id,
    @http_method, @http_protocol, @received_at
);

-- name: UpdateMCPTraceRequestCoderRequestID :exec
UPDATE mcp_trace_requests
SET coder_request_id = @coder_request_id
WHERE id = @id;

-- name: UpdateMCPTraceRequestAuthenticated :exec
UPDATE mcp_trace_requests
SET user_id = COALESCE(@user_id, user_id),
    authenticated_at = @authenticated_at,
    last_stage = 'authenticated'
WHERE id = @id;

-- name: UpdateMCPTraceRequestTransportEntered :exec
UPDATE mcp_trace_requests
SET session_id = CASE WHEN sqlc.arg(session_id)::text = '' THEN session_id ELSE sqlc.arg(session_id)::text END,
    transport_entered_at = @transport_entered_at,
    last_stage = 'transport_entered'
WHERE id = @id;

-- name: UpdateMCPTraceRequestParsed :exec
UPDATE mcp_trace_requests
SET mcp_method = @mcp_method,
    jsonrpc_id = @jsonrpc_id,
    session_id = CASE WHEN sqlc.arg(session_id)::text = '' THEN session_id ELSE sqlc.arg(session_id)::text END,
    parsed_at = @parsed_at,
    last_stage = 'parsed'
WHERE id = @id;

-- name: UpdateMCPTraceRequestSessionRegistered :exec
UPDATE mcp_trace_requests
SET session_id = CASE WHEN sqlc.arg(session_id)::text = '' THEN session_id ELSE sqlc.arg(session_id)::text END,
    session_registered_at = @registered_at,
    last_stage = 'session_registered'
WHERE id = @id;

-- name: UpdateMCPTraceRequestSessionUnregistered :exec
UPDATE mcp_trace_requests
SET session_id = CASE WHEN sqlc.arg(session_id)::text = '' THEN session_id ELSE sqlc.arg(session_id)::text END,
    session_unregistered_at = @unregistered_at,
    last_stage = 'session_unregistered'
WHERE id = @id;

-- name: UpdateMCPTraceRequestDispatched :exec
UPDATE mcp_trace_requests
SET mcp_method = @mcp_method,
    jsonrpc_id = @jsonrpc_id,
    tool = CASE WHEN sqlc.arg(tool)::text = '' THEN tool ELSE sqlc.arg(tool)::text END,
    dispatched_at = @dispatched_at,
    last_stage = 'dispatched'
WHERE id = @id;

-- name: UpdateMCPTraceRequestHandlerStarted :exec
UPDATE mcp_trace_requests
SET tool = CASE WHEN sqlc.arg(tool)::text = '' THEN tool ELSE sqlc.arg(tool)::text END,
    handler_started_at = @handler_started_at,
    last_stage = 'handler_started'
WHERE id = @id;

-- name: UpdateMCPTraceRequestHandlerFinished :exec
UPDATE mcp_trace_requests
SET handler_finished_at = @handler_finished_at,
    last_stage = 'handler_finished'
WHERE id = @id;

-- name: UpdateMCPTraceRequestMCPFinished :exec
UPDATE mcp_trace_requests
SET mcp_finished_at = @mcp_finished_at,
    last_stage = 'mcp_finished'
WHERE id = @id;

-- name: UpdateMCPTraceRequestResponseProgress :exec
UPDATE mcp_trace_requests
SET response_started_at = COALESCE(response_started_at, @write_at),
    last_response_write_at = @write_at,
    response_bytes = response_bytes + @bytes_written,
    response_write_count = response_write_count + CASE WHEN @bytes_written > 0 THEN 1 ELSE 0 END,
    last_stage = 'response_writing'
WHERE id = @id
  AND finished_at IS NULL;

-- name: FinishMCPTraceRequest :exec
UPDATE mcp_trace_requests
SET status = @status,
    error_kind = @error_kind,
    http_status = @http_status,
    response_started_at = COALESCE(@response_started_at, response_started_at),
    last_response_write_at = COALESCE(@last_response_write_at, last_response_write_at),
    response_bytes = GREATEST(response_bytes, @response_bytes),
    response_write_count = GREATEST(response_write_count, @response_write_count),
    canceled_at = @canceled_at,
    finished_at = @finished_at,
    last_stage = @last_stage
WHERE id = @id;

-- name: InsertMCPTraceConnection :exec
INSERT INTO mcp_trace_connections (
    id, request_id, replica_id, user_id, session_id, http_protocol, opened_at
) VALUES (
    @id, @request_id, @replica_id, @user_id, @session_id, @http_protocol, @opened_at
)
ON CONFLICT (request_id) DO NOTHING;

-- name: UpdateMCPTraceConnectionSession :exec
UPDATE mcp_trace_connections
SET user_id = COALESCE(@user_id, user_id),
    session_id = CASE WHEN sqlc.arg(session_id)::text = '' THEN session_id ELSE sqlc.arg(session_id)::text END
WHERE request_id = @request_id;

-- name: FinishMCPTraceConnection :exec
UPDATE mcp_trace_connections
SET status = @status,
    closed_at = @closed_at,
    close_reason = @close_reason
WHERE request_id = @request_id;

-- name: GetMCPTraceRequestByID :one
SELECT * FROM mcp_trace_requests WHERE id = @id;

-- name: GetMCPTraceConnectionByRequestID :one
SELECT * FROM mcp_trace_connections WHERE request_id = @request_id;

-- name: DeleteOldMCPTraceRequests :execrows
DELETE FROM mcp_trace_requests AS target
WHERE target.id IN (
    SELECT candidate.id
    FROM mcp_trace_requests AS candidate
    WHERE candidate.received_at < @before_time
    ORDER BY candidate.received_at ASC
    LIMIT @limit_count
);
