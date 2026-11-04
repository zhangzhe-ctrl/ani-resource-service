-- A synchronous unsupported result is a durable idempotency decision. It must
-- be inserted under the same serialization locks as transfer acceptance.
CREATE TABLE sandbox.checkpoint_transfer_rejections (
 tenant_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN ('restore','clone')),
 target_sandbox_id uuid NOT NULL,
 source_sandbox_id uuid NOT NULL,
 checkpoint_id uuid NOT NULL,
 operation_id uuid NOT NULL,
 create_operation_id uuid,
 idempotency_key text NOT NULL,
 request_digest text NOT NULL,
 reason text NOT NULL CHECK(reason='SANDBOX_TRANSFER_WFFC_UNSUPPORTED'),
 CHECK(kind <> 'clone' OR create_operation_id IS NOT NULL),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,target_sandbox_id,kind,idempotency_key)
);
CREATE UNIQUE INDEX checkpoint_reject_clone_source ON sandbox.checkpoint_transfer_rejections(tenant_id,source_sandbox_id,checkpoint_id,idempotency_key) WHERE kind='clone';
CREATE UNIQUE INDEX checkpoint_reject_clone_target ON sandbox.checkpoint_transfer_rejections(tenant_id,target_sandbox_id) WHERE kind='clone';
CREATE UNIQUE INDEX checkpoint_reject_clone_operation ON sandbox.checkpoint_transfer_rejections(tenant_id,operation_id) WHERE kind='clone';
CREATE UNIQUE INDEX checkpoint_reject_clone_create_operation ON sandbox.checkpoint_transfer_rejections(tenant_id,create_operation_id) WHERE kind='clone';
