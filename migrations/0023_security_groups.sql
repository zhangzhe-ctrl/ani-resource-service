CREATE TABLE network_security_groups (
 tenant_id uuid NOT NULL, security_group_id text NOT NULL CHECK(security_group_id ~ '^sg_[0-9a-f]{32}$'),
 version bigint NOT NULL CHECK(version>0), state text NOT NULL,
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 PRIMARY KEY(tenant_id,security_group_id)
);
CREATE TABLE network_security_group_targets (
 tenant_id uuid NOT NULL, target_type text NOT NULL CHECK(target_type IN ('eip','container','vm')),
 target_id text NOT NULL, version bigint NOT NULL CHECK(version>0),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 PRIMARY KEY(tenant_id,target_type,target_id)
);
CREATE TABLE network_security_group_references (
 tenant_id uuid NOT NULL, target_type text NOT NULL, target_id text NOT NULL,
 security_group_id text NOT NULL, released boolean NOT NULL DEFAULT false,
 PRIMARY KEY(tenant_id,target_type,target_id,security_group_id),
 FOREIGN KEY(tenant_id,security_group_id) REFERENCES network_security_groups(tenant_id,security_group_id),
 FOREIGN KEY(tenant_id,target_type,target_id) REFERENCES network_security_group_targets(tenant_id,target_type,target_id)
);
CREATE TABLE network_security_group_operations (
 tenant_id uuid NOT NULL, operation_id uuid NOT NULL,
 actor_ref text NOT NULL DEFAULT '', caller_ref text NOT NULL DEFAULT '', intent jsonb NOT NULL,
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 PRIMARY KEY(tenant_id,operation_id)
);
CREATE TABLE network_security_group_idempotency (
 tenant_id uuid NOT NULL, action text NOT NULL, idempotency_key text NOT NULL,
 fingerprint text NOT NULL, response jsonb NOT NULL,
 PRIMARY KEY(tenant_id,action,idempotency_key)
);
CREATE TABLE network_security_group_reconciliations (
 tenant_id uuid NOT NULL, target_type text NOT NULL CHECK(target_type IN ('security_group','eip')),
 target_id text NOT NULL, requested_version bigint NOT NULL,
 next_run_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease_owner uuid, lease_until timestamptz, lease_epoch bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(tenant_id,target_type,target_id),
 CHECK((lease_owner IS NULL)=(lease_until IS NULL))
);
CREATE INDEX network_security_group_due ON network_security_group_reconciliations(next_run_at);
