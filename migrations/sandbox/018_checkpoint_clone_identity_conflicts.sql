-- These receipts apply only to an exact clone command. They never reserve an
-- identity already owned by another operation or change its replay behavior.
CREATE TABLE sandbox.checkpoint_clone_identity_conflicts (
 tenant_id uuid NOT NULL,
 request_digest text NOT NULL,
 idempotency_key text NOT NULL,
 source_sandbox_id uuid NOT NULL,
 checkpoint_id uuid NOT NULL,
 target_sandbox_id uuid NOT NULL,
 operation_id uuid NOT NULL,
 create_operation_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id, request_digest, idempotency_key)
);
