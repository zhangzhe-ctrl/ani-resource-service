-- name: ListLBListenerRegistry :many
SELECT * FROM network_lb_listeners WHERE tenant_id=$1 AND lb_id=$2 ORDER BY name;
-- name: ListLBConfigurationListeners :many
SELECT sqlc.embed(l),sqlc.embed(c) FROM network_lb_listeners l
JOIN network_lb_configuration_listeners c USING(tenant_id,lb_id,listener_id)
WHERE c.tenant_id=$1 AND c.lb_id=$2 AND c.config_version=$3 ORDER BY l.name;
-- name: ListLBListenerMembers :many
SELECT sqlc.embed(m),lm.weight FROM network_lb_listener_members lm
JOIN network_lb_members m USING(tenant_id,lb_id,member_id)
WHERE lm.tenant_id=$1 AND lm.lb_id=$2 AND lm.config_version=$3 AND lm.listener_id=$4 ORDER BY m.member_id;
-- name: InsertLBConfigurationListener :exec
INSERT INTO network_lb_configuration_listeners(tenant_id,cluster_id,namespace,lb_id,config_version,listener_id,protocol,port,interval_seconds,timeout_seconds,unhealthy_threshold,healthy_threshold,health_check_port)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13);
-- name: InsertLBListenerMember :exec
INSERT INTO network_lb_listener_members(tenant_id,lb_id,config_version,listener_id,member_id,weight)
VALUES($1,$2,$3,$4,$5,$6);
