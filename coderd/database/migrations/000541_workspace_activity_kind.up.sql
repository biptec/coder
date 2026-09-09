ALTER TABLE workspace_command_activity
    ADD COLUMN kind text NOT NULL DEFAULT 'command'
    CHECK (kind IN ('command', 'tool'));
