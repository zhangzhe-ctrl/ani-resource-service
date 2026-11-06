-- The checkpoint row is the authoritative ownership and write journal. A
-- Kubernetes label/name by itself is never evidence of tenant ownership.
CREATE TABLE sandbox.checkpoints (
 tenant_id uuid NOT NULL,
 sandbox_id uuid NOT NULL,
 checkpoint_id uuid NOT NULL,
 operation_id uuid NOT NULL,
 name text NOT NULL,
 idempotency_key text NOT NULL,
 request_digest text NOT NULL,
 state text NOT NULL CHECK(state IN ('accepted','running','unknown','ready','failed')),
 phase text NOT NULL CHECK(phase IN ('preflight','protect','create','observe','release','done')),
 reason text NOT NULL DEFAULT '',
 failure_reason text NOT NULL DEFAULT '',
 source jsonb NOT NULL,
 snapshot_name text NOT NULL DEFAULT '',
 snapshot_uid text NOT NULL DEFAULT '',
 content_name text NOT NULL DEFAULT '',
 content_uid text NOT NULL DEFAULT '',
 snapshot_handle text NOT NULL DEFAULT '',
 mutation_id text NOT NULL DEFAULT '',
 mutation_kind text NOT NULL DEFAULT '',
 mutation_state text NOT NULL DEFAULT '',
 protection_mutation_id text NOT NULL DEFAULT '',
 snapshot_mutation_id text NOT NULL DEFAULT '',
 release_mutation_id text NOT NULL DEFAULT '',
 cleanup_state text NOT NULL DEFAULT '',
 cleanup_mutation_id text NOT NULL DEFAULT '',
 lease_owner text NOT NULL DEFAULT '',
 lease_epoch bigint NOT NULL DEFAULT 0,
 lease_until timestamptz NOT NULL DEFAULT '-infinity',
 due_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 completed_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,checkpoint_id),
 UNIQUE(tenant_id,sandbox_id,idempotency_key),
 UNIQUE(tenant_id,operation_id),
 FOREIGN KEY(tenant_id,sandbox_id) REFERENCES sandbox.sandboxes(tenant_id,sandbox_id),
 FOREIGN KEY(tenant_id,operation_id) REFERENCES sandbox.operations(tenant_id,operation_id)
);
CREATE INDEX sandbox_checkpoints_list ON sandbox.checkpoints(tenant_id,sandbox_id,created_at DESC,checkpoint_id DESC);
CREATE UNIQUE INDEX sandbox_one_inflight_checkpoint ON sandbox.checkpoints(tenant_id,sandbox_id)
 WHERE state IN ('accepted','running','unknown');
