-- Cover the custom workspace activity/volume tables added in migrations
-- 000537-000542 as well as the MCP trace tables introduced by 000543.
-- The workspace/agent IDs below come from the initial v0.6.6 fixture.

INSERT INTO workspace_volume_copy_operations (
    id, created_at, updated_at, initiator_id,
    source_workspace_id, destination_workspace_id,
    allow_source_running, volumes, status, namespace, job_name,
    error, started_at, completed_at
) VALUES (
    '54300000-0000-4000-8000-000000000001',
    '2026-09-10 10:00:00+00',
    '2026-09-10 10:00:01+00',
    '30095c71-380b-457a-8995-97b8ee6e5307',
    '3a9a1feb-e89d-457c-9d53-ac751b198ebe',
    'b90547be-8870-4d68-8184-e8b2242b7c01',
    false,
    '[{"source":"home","destination":"home-copy"}]'::jsonb,
    'succeeded',
    'coder-workspaces',
    'fixture-volume-copy',
    '',
    '2026-09-10 10:00:00+00',
    '2026-09-10 10:00:01+00'
);

INSERT INTO workspace_volume_copy_locks (
    workspace_id, operation_id, created_at
) VALUES (
    '3a9a1feb-e89d-457c-9d53-ac751b198ebe',
    '54300000-0000-4000-8000-000000000001',
    '2026-09-10 10:00:00+00'
);

INSERT INTO workspace_command_activity (
    id, workspace_id, agent_id, session_id, source,
    command, argv, work_dir, status, started_at, finished_at,
    exit_code, tool, kind
) VALUES (
    '54300000-0000-4000-8000-000000000002',
    '3a9a1feb-e89d-457c-9d53-ac751b198ebe',
    '8fa17bbd-c48c-44c7-91ae-d4acbc755fad',
    '54300000-0000-4000-8000-000000000003',
    'mcp',
    'echo fixture',
    ARRAY['echo', 'fixture'],
    '/home/coder',
    'succeeded',
    '2026-09-10 10:01:00+00',
    '2026-09-10 10:01:01+00',
    0,
    'exec',
    'command'
);

INSERT INTO workspace_connection_activity (
    workspace_id, agent_id, type,
    last_connected_at, last_disconnected_at, last_activity_at
) VALUES (
    '3a9a1feb-e89d-457c-9d53-ac751b198ebe',
    '8fa17bbd-c48c-44c7-91ae-d4acbc755fad',
    'mcp',
    '2026-09-10 10:02:00+00',
    '2026-09-10 10:03:00+00',
    '2026-09-10 10:03:00+00'
);

INSERT INTO workspace_active_connections (
    workspace_id, agent_id, connection_id, type, connected_at
) VALUES (
    '3a9a1feb-e89d-457c-9d53-ac751b198ebe',
    '8fa17bbd-c48c-44c7-91ae-d4acbc755fad',
    '54300000-0000-4000-8000-000000000004',
    'mcp',
    '2026-09-10 10:02:00+00'
);

INSERT INTO workspace_mcp_request_activity (
    id, workspace_id, replica_id, tool, input,
    correlation_hash, status, started_at, finished_at
) VALUES (
    '54300000-0000-4000-8000-000000000005',
    '3a9a1feb-e89d-457c-9d53-ac751b198ebe',
    '54300000-0000-4000-8000-000000000006',
    'exec',
    '{"workspace":"my-workspace"}',
    'fixture-correlation',
    'succeeded',
    '2026-09-10 10:04:00+00',
    '2026-09-10 10:04:01+00'
);

INSERT INTO mcp_trace_requests (
    id, replica_id, coder_request_id, session_id,
    http_method, http_protocol, mcp_method, jsonrpc_id, tool,
    status, last_stage, http_status, response_bytes,
    response_write_count, received_at, authenticated_at,
    transport_entered_at, parsed_at, dispatched_at,
    handler_started_at, handler_finished_at, mcp_finished_at,
    response_started_at, last_response_write_at,
    session_registered_at, session_unregistered_at, finished_at
) VALUES (
    '54300000-0000-4000-8000-000000000007',
    '54300000-0000-4000-8000-000000000006',
    '54300000-0000-4000-8000-000000000008',
    'fixture-session',
    'POST',
    'HTTP/2.0',
    'tools/call',
    '42',
    'exec',
    'completed',
    'response_finished',
    200,
    64,
    1,
    '2026-09-10 10:05:00+00',
    '2026-09-10 10:05:00.010+00',
    '2026-09-10 10:05:00.020+00',
    '2026-09-10 10:05:00.030+00',
    '2026-09-10 10:05:00.040+00',
    '2026-09-10 10:05:00.050+00',
    '2026-09-10 10:05:00.060+00',
    '2026-09-10 10:05:00.070+00',
    '2026-09-10 10:05:00.080+00',
    '2026-09-10 10:05:00.090+00',
    '2026-09-10 10:05:00.035+00',
    '2026-09-10 10:05:00.095+00',
    '2026-09-10 10:05:00.100+00'
);

INSERT INTO mcp_trace_connections (
    id, request_id, replica_id, session_id, http_protocol,
    status, opened_at, closed_at, close_reason
) VALUES (
    '54300000-0000-4000-8000-000000000009',
    '54300000-0000-4000-8000-000000000007',
    '54300000-0000-4000-8000-000000000006',
    'fixture-session',
    'HTTP/2.0',
    'completed',
    '2026-09-10 10:05:00+00',
    '2026-09-10 10:05:00.100+00',
    'handler_returned'
);
