-- Existing users keep the full remote MCP toolset when upgrading.
INSERT INTO user_configs (user_id, key, value)
SELECT id, 'mcp_toolset', 'admin'
FROM users
ON CONFLICT ON CONSTRAINT user_configs_pkey DO NOTHING;
