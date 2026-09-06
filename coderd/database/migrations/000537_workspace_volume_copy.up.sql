CREATE TABLE workspace_volume_copy_operations (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    initiator_id uuid NOT NULL,
    source_workspace_id uuid NOT NULL,
    destination_workspace_id uuid NOT NULL,
    allow_source_running boolean NOT NULL DEFAULT false,
    volumes jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'canceled')),
    namespace text NOT NULL DEFAULT '',
    job_name text NOT NULL DEFAULT '',
    error text NOT NULL DEFAULT '',
    started_at timestamptz,
    completed_at timestamptz,
    sync_of uuid REFERENCES workspace_volume_copy_operations(id) ON DELETE SET NULL,
    CHECK (source_workspace_id <> destination_workspace_id)
);

CREATE INDEX workspace_volume_copy_operations_source_idx
    ON workspace_volume_copy_operations (source_workspace_id, created_at DESC);

CREATE INDEX workspace_volume_copy_operations_destination_idx
    ON workspace_volume_copy_operations (destination_workspace_id, created_at DESC);

CREATE TABLE workspace_volume_copy_locks (
    workspace_id uuid PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    operation_id uuid NOT NULL REFERENCES workspace_volume_copy_operations(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL
);

CREATE INDEX workspace_volume_copy_locks_operation_idx
    ON workspace_volume_copy_locks (operation_id);
