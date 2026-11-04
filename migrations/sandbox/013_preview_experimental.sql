-- Experimental preview state; this migration alone never enables NodePort.
CREATE TABLE sandbox.preview_ports (
 tenant_id uuid NOT NULL,
 sandbox_id uuid NOT NULL,
 preview_id uuid NOT NULL,
 open_operation_id uuid NOT NULL,
 idempotency_key text NOT NULL,
 request_digest text NOT NULL,
 sandbox_version bigint NOT NULL CHECK (sandbox_version > 0),
 pod_uid text NOT NULL CHECK (pod_uid <> ''),
 port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
 protocol text NOT NULL CHECK (protocol IN ('http','tcp')),
 desired text NOT NULL CHECK (desired IN ('open','closed')),
 state text NOT NULL CHECK (state IN ('queued','opening','ready','closing','closed','unknown')),
 reason text NOT NULL DEFAULT '',
 service_uid text NOT NULL DEFAULT '',
 create_mutation_id text NOT NULL DEFAULT '',
 slice_uid text NOT NULL DEFAULT '',
 slice_create_mutation_id text NOT NULL DEFAULT '',
 pod_ip text NOT NULL DEFAULT '',
 address text NOT NULL DEFAULT '',
 pending_mutation jsonb,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,preview_id),
 UNIQUE(tenant_id,sandbox_id,idempotency_key),
 UNIQUE(tenant_id,open_operation_id),
 FOREIGN KEY(tenant_id,sandbox_id) REFERENCES sandbox.sandboxes(tenant_id,sandbox_id)
);
-- One listener per Sandbox port; HTTP and TCP cannot simultaneously own it.
CREATE UNIQUE INDEX preview_one_live_port ON sandbox.preview_ports(tenant_id,sandbox_id,port) WHERE state <> 'closed';
CREATE INDEX preview_cleanup_due ON sandbox.preview_ports(expires_at) WHERE state <> 'closed';
CREATE TABLE sandbox.preview_port_operations (
 tenant_id uuid NOT NULL,
 sandbox_id uuid NOT NULL,
 preview_id uuid NOT NULL,
 operation_id uuid NOT NULL,
 action text NOT NULL CHECK(action IN ('open','close')),
 idempotency_key text NOT NULL,
 request_digest text NOT NULL,
 state text NOT NULL CHECK(state IN ('queued','running','succeeded','failed','unknown')),
 reason text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 completed_at timestamptz,
 PRIMARY KEY(tenant_id,operation_id),
 UNIQUE(tenant_id,preview_id,action,idempotency_key),
 FOREIGN KEY(tenant_id,preview_id) REFERENCES sandbox.preview_ports(tenant_id,preview_id)
);
