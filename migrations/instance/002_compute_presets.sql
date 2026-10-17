CREATE TABLE instance.compute_presets (
 id uuid PRIMARY KEY,
 code text NOT NULL UNIQUE CHECK(code ~ '^[a-z][a-z0-9-]{0,63}$'),
 name text NOT NULL CHECK(length(btrim(name))>0 AND length(name)<=128),
 description text NOT NULL DEFAULT '' CHECK(length(description)<=1024),
 compute_type text NOT NULL CHECK(compute_type IN ('cpu','gpu')),
 cpu_millis bigint NOT NULL CHECK(cpu_millis>0),
 memory_mib bigint NOT NULL CHECK(memory_mib>0 AND memory_mib<=8796093022207),
 enabled boolean NOT NULL DEFAULT false,
 gpu jsonb,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_by text NOT NULL CHECK(length(created_by)>0),
 updated_by text NOT NULL CHECK(length(updated_by)>0),
 CHECK ((compute_type='cpu' AND gpu IS NULL) OR
        (compute_type='gpu' AND gpu IS NOT NULL AND jsonb_typeof(gpu)='object'
         AND jsonb_strip_nulls(gpu) ?& ARRAY['profile_id','profile_version','spec_digest','devices_per_instance','cluster_id','pool_id','mode','model_key','core_limit_percent']
         AND (gpu->>'profile_version')::numeric BETWEEN 1 AND 18446744073709551615
         AND (gpu->>'profile_id')::uuid IS NOT NULL AND (gpu->>'cluster_id')::uuid IS NOT NULL AND (gpu->>'pool_id')::uuid IS NOT NULL
         AND gpu->>'spec_digest' ~ '^[0-9a-f]{64}$' AND length(btrim(gpu->>'model_key'))>0
         AND length(gpu->>'spec_digest')=64
         AND ((gpu->>'mode'='WHOLE_EXCLUSIVE' AND (gpu->>'devices_per_instance')::integer IN (1,2) AND (gpu->>'core_limit_percent')::integer=100 AND (gpu->'shared_memory_mib_per_device' IS NULL OR gpu->'shared_memory_mib_per_device'='null'::jsonb))
           OR (gpu->>'mode'='SHARED_FIXED' AND (gpu->>'devices_per_instance')::integer=1 AND jsonb_typeof(gpu->'shared_memory_mib_per_device')='number' AND (gpu->>'shared_memory_mib_per_device')::bigint>0 AND (gpu->>'core_limit_percent')::integer BETWEEN 1 AND 99))))
);
CREATE INDEX compute_presets_listing ON instance.compute_presets(created_at DESC,id DESC);
