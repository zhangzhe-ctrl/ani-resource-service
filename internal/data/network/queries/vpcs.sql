-- name: DatabaseTime :one
SELECT clock_timestamp()::timestamptz AS now;

-- name: LockCreationKey :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(tenant_id)::text || ':create_vpc:' || sqlc.arg(idempotency_key)::text, 0));

-- name: GetIdempotency :one
SELECT * FROM network_idempotency
WHERE tenant_id = $1 AND operation_kind = 'create_vpc' AND idempotency_key = $2;

-- name: InsertVPC :one
INSERT INTO network_vpcs (tenant_id,vpc_id,name,description,cidr,state,created_at,updated_at,last_operation_id)
VALUES ($1,$2,$3,$4,$5,'provisioning',$6,$6,$7)
RETURNING *;

-- name: InsertOperation :exec
INSERT INTO network_operations (tenant_id,operation_id,vpc_id,subnet_id,eip_id,snat_id,lb_id,kind,state,created_at,updated_at,next_attempt_at)
VALUES (sqlc.arg(tenant_id),sqlc.arg(operation_id),NULLIF(sqlc.arg(vpc_id)::text,''),NULLIF(sqlc.arg(subnet_id)::text,''),NULLIF(sqlc.arg(eip_id)::text,''),NULLIF(sqlc.arg(snat_id)::text,''),NULLIF(sqlc.arg(lb_id)::text,''),sqlc.arg(kind),'queued',sqlc.arg(created_at),sqlc.arg(created_at),sqlc.arg(created_at));

-- name: InsertReconciliation :exec
INSERT INTO network_reconciliations (tenant_id,vpc_id,subnet_id,eip_id,snat_id,lb_id,next_run_at) VALUES (sqlc.arg(tenant_id),NULLIF(sqlc.arg(vpc_id)::text,''),NULLIF(sqlc.arg(subnet_id)::text,''),NULLIF(sqlc.arg(eip_id)::text,''),NULLIF(sqlc.arg(snat_id)::text,''),NULLIF(sqlc.arg(lb_id)::text,''),sqlc.arg(next_run_at));

-- name: InsertBinding :exec
INSERT INTO network_provider_bindings (tenant_id,vpc_id,subnet_id,eip_id,snat_id,lb_id,binding_id,cluster_id,namespace,provider_name,create_dispatched,resource_kind)
VALUES (sqlc.arg(tenant_id),NULLIF(sqlc.arg(vpc_id)::text,''),NULLIF(sqlc.arg(subnet_id)::text,''),NULLIF(sqlc.arg(eip_id)::text,''),NULLIF(sqlc.arg(snat_id)::text,''),NULLIF(sqlc.arg(lb_id)::text,''),sqlc.arg(binding_id),sqlc.arg(cluster_id),sqlc.arg(namespace),sqlc.arg(provider_name),false,CASE WHEN sqlc.arg(lb_id)::text<>'' THEN 'load_balancer' WHEN sqlc.arg(eip_id)::text<>'' THEN 'eip' WHEN sqlc.arg(snat_id)::text<>'' THEN 'snat' WHEN sqlc.arg(subnet_id)::text<>'' THEN 'subnet' ELSE 'vpc' END);

-- name: InsertIdempotency :exec
INSERT INTO network_idempotency
(tenant_id,operation_kind,idempotency_key,fingerprint,fingerprint_version,vpc_id,operation_id,response,created_at)
VALUES (sqlc.arg(tenant_id),'create_vpc',sqlc.arg(idempotency_key),sqlc.arg(fingerprint),1,sqlc.arg(vpc_id)::text,sqlc.arg(operation_id),sqlc.arg(response),sqlc.arg(created_at));

-- name: InsertHistory :exec
INSERT INTO network_resource_history
(tenant_id,history_id,vpc_id,subnet_id,eip_id,snat_id,lb_id,operation_id,event,resource_state,operation_state,reason,actor_ref,caller_ref,correlation_id,created_at)
VALUES (sqlc.arg(tenant_id),sqlc.arg(history_id),NULLIF(sqlc.arg(vpc_id)::text,''),NULLIF(sqlc.arg(subnet_id)::text,''),NULLIF(sqlc.arg(eip_id)::text,''),NULLIF(sqlc.arg(snat_id)::text,''),NULLIF(sqlc.arg(lb_id)::text,''),sqlc.narg(operation_id),sqlc.arg(event),sqlc.arg(resource_state),sqlc.arg(operation_state),sqlc.arg(reason),sqlc.arg(actor_ref),sqlc.arg(caller_ref),sqlc.arg(correlation_id),sqlc.arg(created_at));

-- name: GetVPC :one
SELECT sqlc.embed(v),coalesce(b.state,'missing')::text AS base_state,coalesce(b.reason,'')::text AS base_reason,b.observed_at AS base_observed_at, (SELECT count(*) FROM network_subnets s WHERE s.tenant_id=v.tenant_id AND s.vpc_id=v.vpc_id AND s.state<>'deleted')::bigint AS subnet_count FROM network_vpcs v LEFT JOIN network_vpc_base_connectivity b ON b.tenant_id=v.tenant_id AND b.vpc_id=v.vpc_id WHERE v.tenant_id=$1 AND v.vpc_id=$2;

-- name: GetOperation :one
SELECT * FROM network_operations WHERE tenant_id = $1 AND operation_id = $2;

-- name: ListVPCs :many
SELECT sqlc.embed(v),coalesce(b.state,'missing')::text AS base_state,coalesce(b.reason,'')::text AS base_reason,b.observed_at AS base_observed_at, (SELECT count(*) FROM network_subnets s WHERE s.tenant_id=v.tenant_id AND s.vpc_id=v.vpc_id AND s.state<>'deleted')::bigint AS subnet_count FROM network_vpcs v LEFT JOIN network_vpc_base_connectivity b ON b.tenant_id=v.tenant_id AND b.vpc_id=v.vpc_id
WHERE v.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.arg(name_filter)::text = '' OR v.name = sqlc.arg(name_filter))
  AND ((sqlc.arg(state_filter)::text = '' AND v.state <> 'deleted') OR v.state = sqlc.arg(state_filter))
  AND (sqlc.arg(after_id)::text = '' OR (v.created_at, v.vpc_id) < (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::text))
ORDER BY v.created_at DESC, v.vpc_id DESC
LIMIT sqlc.arg(max_results)::integer;

-- name: EnsureTenantNamespace :exec
INSERT INTO network_tenant_namespaces(tenant_id,cluster_id,namespace) VALUES ($1,$2,$3)
ON CONFLICT (tenant_id,cluster_id) DO NOTHING;

-- name: GetTenantNamespace :one
SELECT namespace FROM network_tenant_namespaces WHERE tenant_id=$1 AND cluster_id=$2;

-- name: CountVPCs :one
SELECT count(*) FROM network_vpcs v
WHERE v.tenant_id = sqlc.arg(tenant_id)
 AND (sqlc.arg(name_filter)::text = '' OR v.name = sqlc.arg(name_filter))
 AND ((sqlc.arg(state_filter)::text = '' AND v.state <> 'deleted') OR v.state = sqlc.arg(state_filter));
