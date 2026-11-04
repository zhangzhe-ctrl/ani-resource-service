-- Old file operations do not retain the original request action or a reliable
-- completion timestamp. Keep both NULL instead of guessing from the digest,
-- result or updated_at. New admissions record only their non-sensitive action.
ALTER TABLE sandbox.file_operations
    ADD COLUMN action text CHECK (action IN ('write', 'delete')),
    ADD COLUMN completed_at timestamptz;

CREATE INDEX sandbox_file_operation_history ON sandbox.file_operations
    (tenant_id, sandbox_id, created_at DESC, operation_id DESC);
CREATE INDEX sandbox_code_run_history ON sandbox.code_runs
    (tenant_id, sandbox_id, created_at DESC, run_id DESC);
