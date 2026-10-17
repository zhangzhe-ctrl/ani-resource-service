-- Explicit one-time cutover. Operations and original audit rows remain intact.
-- Close old activity before applying; the new service does not decode old GPU snapshots.
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM instance.instances WHERE snapshot->'Spec'->>'compute_type'='gpu' AND (desired <> 'deleted' OR snapshot->>'ComputeCleaned' IS DISTINCT FROM 'true' OR snapshot->>'WorkspaceCleaned' IS DISTINCT FROM 'true' OR snapshot->>'QuotaReturned' IS DISTINCT FROM 'true'))
 OR EXISTS (SELECT 1 FROM instance.instance_quota_release_outbox WHERE NOT acknowledged)
 THEN RAISE EXCEPTION 'GPU cutover requires closed instances and acknowledged release outbox'; END IF;
END $$;
ALTER TABLE instance.instance_quota_release_outbox RENAME COLUMN charges TO archived_charges;
ALTER TABLE instance.instance_quota_release_outbox ALTER COLUMN archived_charges DROP NOT NULL;
ALTER TABLE instance.instance_quota_release_outbox ADD COLUMN reason text NOT NULL DEFAULT 'RESOURCE_RELEASED' CHECK(reason IN ('RESOURCE_RELEASED','ABORTED_CLEANED'));
ALTER TABLE instance.compute_presets DROP CONSTRAINT compute_presets_check;
UPDATE instance.compute_presets SET enabled=false WHERE compute_type='gpu';
ALTER TABLE instance.compute_presets ADD CONSTRAINT compute_presets_check CHECK (
 (compute_type='cpu' AND gpu IS NULL) OR
 (compute_type='gpu' AND gpu IS NOT NULL AND jsonb_typeof(gpu)='object'
 AND jsonb_strip_nulls(gpu) ?& ARRAY['profile_id','profile_version','spec_digest','devices_per_instance','cluster_id','pool_id','mode','model_key','memory_mib_per_device','core_limit_percent']
 AND NOT gpu ? 'shared_memory_mib_per_device'
 AND (gpu->>'profile_version')::numeric BETWEEN 1 AND 18446744073709551615
 AND (gpu->>'profile_id')::uuid IS NOT NULL AND (gpu->>'cluster_id')::uuid IS NOT NULL AND (gpu->>'pool_id')::uuid IS NOT NULL
 AND gpu->>'spec_digest' ~ '^[0-9a-f]{64}$' AND length(btrim(gpu->>'model_key'))>0
 AND CASE WHEN jsonb_typeof(gpu->'devices_per_instance')='number' AND gpu->>'devices_per_instance' ~ '^[1-9][0-9]*$' THEN (gpu->>'devices_per_instance')::numeric BETWEEN 1 AND 4294967295 ELSE false END
 AND (gpu->>'memory_mib_per_device')::bigint>0
 AND ((gpu->>'mode'='WHOLE_EXCLUSIVE' AND (gpu->>'core_limit_percent')::integer=100)
 OR (gpu->>'mode'='SHARED_FIXED' AND gpu->>'devices_per_instance'='1' AND (gpu->>'core_limit_percent')::integer BETWEEN 1 AND 99)))) NOT VALID;
-- Existing Presets are not converted from the former F256 contract. They must
-- be explicitly replaced with newly resolved references before re-enabling.

CREATE TABLE instance.gpu_cutover_audit (
 tenant_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 original_snapshot jsonb NOT NULL,
 archived_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,instance_id),
 FOREIGN KEY(tenant_id,instance_id) REFERENCES instance.instances(tenant_id,instance_id)
);
INSERT INTO instance.gpu_cutover_audit(tenant_id,instance_id,original_snapshot)
 SELECT tenant_id,instance_id,snapshot FROM instance.instances;
-- Terminal audit references use the same read path for every closed GPU record.
-- No runtime parser interprets the archived execution schema.
UPDATE instance.instances SET snapshot = jsonb_set(snapshot - 'Charges','{Spec}',
 jsonb_build_object('instance_kind','container','compute_type','gpu'))
 WHERE snapshot->'Spec'->>'compute_type'='gpu';
UPDATE instance.instances SET snapshot = jsonb_set(snapshot - 'Charges','{Spec,GPU}','{}'::jsonb)
 WHERE snapshot->'Spec'->>'compute_type'='cpu';
