-- Applied migrations stay immutable. Whole-device quantity is any positive
-- uint32; the complete ACC plan enforces its Profile-specific upper bound.
ALTER TABLE instance.compute_presets DROP CONSTRAINT compute_presets_check;
ALTER TABLE instance.compute_presets ADD CONSTRAINT compute_presets_check CHECK (
 (compute_type='cpu' AND gpu IS NULL) OR
 (compute_type='gpu' AND gpu IS NOT NULL AND jsonb_typeof(gpu)='object'
  AND jsonb_strip_nulls(gpu) ?& ARRAY['profile_id','profile_version','spec_digest','devices_per_instance','cluster_id','pool_id','mode','model_key','core_limit_percent']
  AND (gpu->>'profile_version')::numeric BETWEEN 1 AND 18446744073709551615
  AND (gpu->>'profile_id')::uuid IS NOT NULL AND (gpu->>'cluster_id')::uuid IS NOT NULL AND (gpu->>'pool_id')::uuid IS NOT NULL
  AND gpu->>'spec_digest' ~ '^[0-9a-f]{64}$' AND length(btrim(gpu->>'model_key'))>0
  AND length(gpu->>'spec_digest')=64
  AND CASE WHEN jsonb_typeof(gpu->'devices_per_instance')='number'
                AND gpu->>'devices_per_instance' ~ '^[1-9][0-9]*$'
           THEN (gpu->>'devices_per_instance')::numeric BETWEEN 1 AND 4294967295
           ELSE false END
  AND ((gpu->>'mode'='WHOLE_EXCLUSIVE' AND (gpu->>'core_limit_percent')::integer=100 AND (gpu->'shared_memory_mib_per_device' IS NULL OR gpu->'shared_memory_mib_per_device'='null'::jsonb))
    OR (gpu->>'mode'='SHARED_FIXED' AND gpu->>'devices_per_instance'='1' AND jsonb_typeof(gpu->'shared_memory_mib_per_device')='number' AND (gpu->>'shared_memory_mib_per_device')::bigint>0 AND (gpu->>'core_limit_percent')::integer BETWEEN 1 AND 99)))
);
