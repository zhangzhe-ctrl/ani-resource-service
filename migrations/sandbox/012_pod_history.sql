-- A first-recorded time is not the Pod creation time. UIDs previously cleared
-- by an older worker cannot be reconstructed from these persisted facts.
CREATE TABLE sandbox.pod_history (
 tenant_id uuid NOT NULL,
 sandbox_id uuid NOT NULL,
 cluster_id text NOT NULL,
 namespace text NOT NULL,
 namespace_uid text NOT NULL,
 pod_name text NOT NULL,
 pod_uid text NOT NULL CHECK (pod_uid <> ''),
 first_recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id, sandbox_id, pod_uid),
 FOREIGN KEY (tenant_id, sandbox_id) REFERENCES sandbox.sandboxes(tenant_id, sandbox_id)
);
INSERT INTO sandbox.pod_history
 (tenant_id,sandbox_id,cluster_id,namespace,namespace_uid,pod_name,pod_uid)
SELECT tenant_id,sandbox_id,COALESCE(snapshot->>'ClusterID',''),COALESCE(snapshot->>'Namespace',''),
 COALESCE(snapshot->>'NamespaceUID',''),COALESCE(provider_refs->>'PodName',''),provider_refs->>'PodUID'
FROM sandbox.sandboxes WHERE COALESCE(provider_refs->>'PodUID','') <> '';
