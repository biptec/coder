-- Aggregate-only production history queries for MCP tool-selection eval design.
--
-- These queries intentionally do not return MCP input, command arguments,
-- command output, environment values, workspace IDs, or remote host names.

SELECT
    count(*) AS requests,
    count(DISTINCT workspace_id) AS workspaces,
    count(DISTINCT tool) AS tools,
    min(started_at) AS first_request,
    max(started_at) AS last_request
FROM workspace_mcp_request_activity;

SELECT
    tool,
    count(*) AS requests,
    count(*) FILTER (WHERE status = 'succeeded') AS succeeded,
    count(*) FILTER (WHERE status = 'failed') AS failed,
    count(*) FILTER (WHERE status = 'interrupted') AS interrupted
FROM workspace_mcp_request_activity
GROUP BY tool
ORDER BY requests DESC, tool;

SELECT
    status,
    count(*) AS requests
FROM workspace_mcp_request_activity
GROUP BY status
ORDER BY requests DESC, status;

-- Historical rows from older implementations may not contain JSON input.
SELECT
    tool,
    count(*) FILTER (WHERE input IS JSON) AS json_rows,
    count(*) FILTER (WHERE NOT (input IS JSON)) AS non_json_rows
FROM workspace_mcp_request_activity
GROUP BY tool
ORDER BY count(*) DESC, tool;

-- Only argument key names are returned, never their values.
SELECT
    requests.tool,
    keys.key,
    count(*) AS uses
FROM workspace_mcp_request_activity AS requests
CROSS JOIN LATERAL jsonb_object_keys(requests.input::jsonb) AS keys(key)
WHERE requests.input IS JSON
  AND jsonb_typeof(requests.input::jsonb) = 'object'
GROUP BY requests.tool, keys.key
ORDER BY requests.tool, uses DESC, keys.key;

-- Executable names are useful for deciding whether a structured capability is
-- common enough to justify a first-class MCP contract. Later argv elements are
-- deliberately excluded.
SELECT
    tool,
    COALESCE(NULLIF(argv[1], ''), '<shell>') AS executable,
    count(*) AS commands,
    count(*) FILTER (WHERE exit_code IS NOT NULL AND exit_code <> 0) AS nonzero_exit
FROM workspace_command_activity
WHERE tool IN ('exec', 'process_start')
GROUP BY tool, executable
ORDER BY commands DESC, tool, executable
LIMIT 100;

-- Classify bash commands server-side and return only broad categories.
SELECT
    CASE
        WHEN lower(command) LIKE '%ssh %' THEN 'ssh'
        WHEN lower(command) LIKE '%curl %' THEN 'curl'
        WHEN lower(command) LIKE '%git %' THEN 'git'
        WHEN lower(command) LIKE '%kubectl %' THEN 'kubectl'
        WHEN lower(command) LIKE '%terraform %' THEN 'terraform'
        WHEN lower(command) LIKE '%make %' THEN 'make'
        WHEN lower(command) LIKE '%go %' THEN 'go'
        WHEN lower(command) LIKE '%python%' THEN 'python'
        ELSE 'other'
    END AS category,
    count(*) AS commands,
    count(*) FILTER (WHERE exit_code IS NOT NULL AND exit_code <> 0) AS nonzero_exit
FROM workspace_command_activity
WHERE tool = 'bash'
GROUP BY category
ORDER BY commands DESC, category;

SELECT
    count(*) FILTER (
        WHERE tool = 'bash' AND lower(command) LIKE '%ssh %'
    ) AS bash_ssh,
    count(*) FILTER (
        WHERE tool = 'bash'
          AND lower(command) LIKE '%ssh %'
          AND lower(command) LIKE '%curl %'
    ) AS bash_ssh_curl,
    count(*) FILTER (
        WHERE tool = 'bash'
          AND lower(command) LIKE '%ssh %'
          AND exit_code IS NOT NULL
          AND exit_code <> 0
    ) AS bash_ssh_nonzero_exit
FROM workspace_command_activity;
