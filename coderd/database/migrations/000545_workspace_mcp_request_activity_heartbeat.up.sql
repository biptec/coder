ALTER TABLE workspace_mcp_request_activity
    ADD COLUMN heartbeat_at timestamptz;

-- Existing running requests may belong to the previous Coder replica during a
-- rolling upgrade. Give them a fresh grace window instead of immediately
-- classifying them as stale. Completed rows use their completion timestamp.
UPDATE workspace_mcp_request_activity
SET heartbeat_at = COALESCE(finished_at, NOW());

ALTER TABLE workspace_mcp_request_activity
    ALTER COLUMN heartbeat_at SET NOT NULL,
    ALTER COLUMN heartbeat_at SET DEFAULT NOW();
