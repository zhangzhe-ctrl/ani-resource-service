-- Versioned listener configuration. Existing IDs, component names and UIDs
-- remain intact; historical idempotency receipts are not rewritten.
ALTER TABLE network_lb_listeners ADD COLUMN name text NOT NULL DEFAULT 'http'
 CHECK (char_length(name) BETWEEN 1 AND 253 AND name ~ '^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$');
ALTER TABLE network_lb_listeners DROP CONSTRAINT network_lb_listeners_pkey;
ALTER TABLE network_lb_listeners ADD PRIMARY KEY (tenant_id,lb_id,listener_id);
ALTER TABLE network_lb_listeners ADD UNIQUE (tenant_id,cluster_id,namespace,lb_id,listener_id);
ALTER TABLE network_lb_listeners ADD UNIQUE (tenant_id,lb_id,name);

CREATE TABLE network_lb_configuration_listeners (
 tenant_id uuid NOT NULL, cluster_id text NOT NULL, namespace text NOT NULL, lb_id text NOT NULL,
 config_version bigint NOT NULL, listener_id uuid NOT NULL,
 protocol text NOT NULL CHECK (protocol='HTTP'), port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
 interval_seconds bigint NOT NULL CHECK (interval_seconds BETWEEN 1 AND 4294967295),
 timeout_seconds bigint NOT NULL CHECK (timeout_seconds BETWEEN 1 AND 4294967295 AND timeout_seconds<interval_seconds),
 unhealthy_threshold bigint NOT NULL CHECK (unhealthy_threshold BETWEEN 1 AND 4294967295),
 healthy_threshold bigint NOT NULL CHECK (healthy_threshold BETWEEN 1 AND 4294967295),
 health_check_port integer NOT NULL CHECK (health_check_port BETWEEN 0 AND 65535),
 PRIMARY KEY (tenant_id,lb_id,config_version,listener_id),
 UNIQUE (tenant_id,lb_id,config_version,port),
 FOREIGN KEY (tenant_id,cluster_id,namespace,lb_id,config_version) REFERENCES network_lb_configurations(tenant_id,cluster_id,namespace,lb_id,config_version),
 FOREIGN KEY (tenant_id,cluster_id,namespace,lb_id,listener_id) REFERENCES network_lb_listeners(tenant_id,cluster_id,namespace,lb_id,listener_id)
);
INSERT INTO network_lb_configuration_listeners
 SELECT c.tenant_id,c.cluster_id,c.namespace,c.lb_id,c.config_version,l.listener_id,l.protocol,l.port,
 c.interval_seconds,c.timeout_seconds,c.unhealthy_threshold,c.healthy_threshold,c.health_check_port
 FROM network_lb_configurations c JOIN network_lb_listeners l USING(tenant_id,cluster_id,namespace,lb_id);

CREATE TABLE network_lb_listener_members (
 tenant_id uuid NOT NULL, lb_id text NOT NULL, config_version bigint NOT NULL,
 listener_id uuid NOT NULL, member_id uuid NOT NULL, weight integer NOT NULL CHECK (weight BETWEEN 0 AND 1000000),
 PRIMARY KEY (tenant_id,lb_id,config_version,listener_id,member_id),
 FOREIGN KEY (tenant_id,lb_id,config_version,listener_id) REFERENCES network_lb_configuration_listeners(tenant_id,lb_id,config_version,listener_id),
 FOREIGN KEY (tenant_id,lb_id,config_version,member_id) REFERENCES network_lb_configuration_members(tenant_id,lb_id,config_version,member_id)
);
INSERT INTO network_lb_listener_members
 SELECT m.tenant_id,m.lb_id,m.config_version,l.listener_id,m.member_id,m.weight
 FROM network_lb_configuration_members m JOIN network_lb_listeners l USING(tenant_id,lb_id);

ALTER TABLE network_lb_components ADD COLUMN listener_id uuid;
UPDATE network_lb_components c SET listener_id=l.listener_id FROM network_lb_listeners l
 WHERE c.tenant_id=l.tenant_id AND c.lb_id=l.lb_id AND c.kind IN ('route','policy');
ALTER TABLE network_lb_components ADD FOREIGN KEY (tenant_id,cluster_id,namespace,lb_id,listener_id)
 REFERENCES network_lb_listeners(tenant_id,cluster_id,namespace,lb_id,listener_id);
ALTER TABLE network_lb_components ADD CHECK ((kind IN ('route','policy'))=(listener_id IS NOT NULL));
DROP INDEX network_lb_route_policy;
CREATE UNIQUE INDEX network_lb_route_policy ON network_lb_components(tenant_id,lb_id,listener_id,kind) WHERE listener_id IS NOT NULL AND deleted_at IS NULL;
