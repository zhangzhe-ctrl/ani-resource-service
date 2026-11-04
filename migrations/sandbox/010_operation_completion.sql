-- Historical terminal operation rows do not have a trustworthy completion
-- timestamp; leave them NULL rather than guessing from another column.
ALTER TABLE sandbox.operations ADD COLUMN completed_at timestamptz;
CREATE INDEX sandbox_operation_history ON sandbox.operations
    (tenant_id, sandbox_id, created_at DESC, operation_id DESC);
