ALTER TABLE workspace_command_activity
    ADD COLUMN environment jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN output text NOT NULL DEFAULT '';
