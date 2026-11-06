-- Candidate: must be registered only together with lifecycle, checkpoint delete
-- and provider workspace-generation support. No runtime code enables this yet.
ALTER TABLE sandbox.checkpoints ADD CONSTRAINT checkpoint_tenant_sandbox_identity UNIQUE(tenant_id,sandbox_id,checkpoint_id);
CREATE TABLE sandbox.checkpoint_transfers (
 tenant_id uuid NOT NULL, transfer_id uuid NOT NULL, operation_id uuid NOT NULL,
 checkpoint_id uuid NOT NULL, source_sandbox_id uuid NOT NULL, target_sandbox_id uuid NOT NULL,
 kind text NOT NULL CHECK(kind IN ('restore','clone')),
 idempotency_key text NOT NULL, request_digest text NOT NULL,
 expected_version bigint NOT NULL, source_expected_version bigint NOT NULL DEFAULT 0, source_snapshot_name text NOT NULL,
 source_snapshot_uid text NOT NULL, source_content_uid text NOT NULL, source_snapshot_handle text NOT NULL,
 original_refs jsonb NOT NULL, candidate_name text NOT NULL, candidate_uid text NOT NULL DEFAULT '',
 candidate_pv_name text NOT NULL DEFAULT '', candidate_pv_uid text NOT NULL DEFAULT '',
 -- A Bound PVC alone does not prove the storage backend has detached from the
 -- source snapshot. This remains false until a future trusted CSI proof exists.
 source_released boolean NOT NULL DEFAULT false,
 state text NOT NULL CHECK(state IN ('accepted','creating','unknown','observing','ready','failed')),
 mutation_id text NOT NULL DEFAULT '', lease_owner text NOT NULL DEFAULT '',
 lease_epoch bigint NOT NULL DEFAULT 0, lease_until timestamptz NOT NULL DEFAULT '-infinity',
 due_at timestamptz NOT NULL DEFAULT clock_timestamp(), reason text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), completed_at timestamptz,
 PRIMARY KEY(tenant_id,transfer_id), UNIQUE(transfer_id), UNIQUE(tenant_id,operation_id), UNIQUE(tenant_id,candidate_name),
 UNIQUE(tenant_id,target_sandbox_id,kind,idempotency_key),
 FOREIGN KEY(tenant_id,checkpoint_id) REFERENCES sandbox.checkpoints(tenant_id,checkpoint_id),
 FOREIGN KEY(tenant_id,source_sandbox_id,checkpoint_id) REFERENCES sandbox.checkpoints(tenant_id,sandbox_id,checkpoint_id),
 FOREIGN KEY(tenant_id,source_sandbox_id) REFERENCES sandbox.sandboxes(tenant_id,sandbox_id),
 FOREIGN KEY(tenant_id,target_sandbox_id) REFERENCES sandbox.sandboxes(tenant_id,sandbox_id),
 FOREIGN KEY(tenant_id,operation_id) REFERENCES sandbox.operations(tenant_id,operation_id)
);
CREATE UNIQUE INDEX checkpoint_one_active_transfer ON sandbox.checkpoint_transfers(tenant_id,target_sandbox_id)
 WHERE state IN ('accepted','creating','unknown','observing');
CREATE INDEX checkpoint_transfer_source ON sandbox.checkpoint_transfers(tenant_id,checkpoint_id,state);
CREATE UNIQUE INDEX checkpoint_clone_replay_scope ON sandbox.checkpoint_transfers(tenant_id,source_sandbox_id,checkpoint_id,idempotency_key) WHERE kind='clone';
CREATE TABLE sandbox.workspace_generations (
 tenant_id uuid NOT NULL, sandbox_id uuid NOT NULL, generation_id uuid NOT NULL,
 pvc_name text NOT NULL, pvc_uid text NOT NULL, pv_name text NOT NULL, pv_uid text NOT NULL,
 state text NOT NULL CHECK(state IN ('active','retired','reclaimed')),
 source_checkpoint_id uuid, source_snapshot_uid text NOT NULL DEFAULT '',
 provider_refs jsonb NOT NULL DEFAULT '{}'::jsonb,
 cleanup_state text NOT NULL DEFAULT '', cleanup_mutation_id text NOT NULL DEFAULT '',
 cleanup_driver text NOT NULL DEFAULT '', cleanup_volume_handle text NOT NULL DEFAULT '',
 cleanup_lease_owner text NOT NULL DEFAULT '', cleanup_lease_epoch bigint NOT NULL DEFAULT 0,
 cleanup_lease_until timestamptz NOT NULL DEFAULT '-infinity', cleanup_due_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 cleanup_completed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,sandbox_id,generation_id),
 FOREIGN KEY(tenant_id,sandbox_id) REFERENCES sandbox.sandboxes(tenant_id,sandbox_id)
);
CREATE UNIQUE INDEX workspace_one_active_generation ON sandbox.workspace_generations(tenant_id,sandbox_id) WHERE state='active';
