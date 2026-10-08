package data_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	"github.com/zhangzhe-ctrl/ani-resource-service/migrations"
	"github.com/zhangzhe-ctrl/ani-resource-service/tests/testenv"
)

func TestLBSchemaFrom0006PreservesAllOldColumnsAndDormantReservations(t *testing.T) {
	ctx := context.Background()
	columns, before := map[string]string{}, map[string]string{}
	var legacy legacyPublicFixture
	lbID, subnetID := schemaResourceID("lb"), schemaResourceID("subnet")
	tables := []string{"network_vpcs", "network_subnets", "network_eips", "network_snat_bindings", "network_eip_claims", "network_load_balancers", "network_lb_vip_intents", "network_provider_bindings", "network_operations", "network_reconciliations", "network_idempotency", "network_resource_history"}
	snapshot := func(owner *pgxpool.Pool, table string) string {
		t.Helper()
		var value string
		query := fmt.Sprintf(`SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]')::text FROM (SELECT %s FROM %s) r`, columns[table], pgx.Identifier{table}.Sanitize())
		if err := owner.QueryRow(ctx, query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	db := testenv.NewDatabase(t, func(owner *pgxpool.Pool, _ string) {
		legacy = seedSchema5Public(t, owner)
		tx, err := owner.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		exec := func(query string, args ...any) {
			t.Helper()
			if _, err := tx.Exec(ctx, query, args...); err != nil {
				t.Fatal(err)
			}
		}
		body, err := migrations.Files.ReadFile("0006_vpc_base_connectivity.sql")
		if err != nil {
			t.Fatal(err)
		}
		exec(string(body))
		sum := sha256.Sum256(body)
		exec(`INSERT INTO network_schema_version(version,checksum) VALUES(6,$1)`, hex.EncodeToString(sum[:]))
		op := uuid.NewString()
		ns := "tenant-" + legacy.Tenant
		exec(`INSERT INTO network_subnets(tenant_id,subnet_id,vpc_id,name,description,cidr,gateway,state,created_at,updated_at,observed_at,last_operation_id) VALUES($1,$2,$3,'old-entry','','10.42.1.0/24','10.42.1.1','available',clock_timestamp(),clock_timestamp(),clock_timestamp(),$4)`, legacy.Tenant, subnetID, legacy.VPC, op)
		exec(`INSERT INTO network_operations(tenant_id,subnet_id,operation_id,kind,state,created_at,updated_at,completed_at) VALUES($1,$2,$3,'create_subnet','succeeded',clock_timestamp(),clock_timestamp(),clock_timestamp())`, legacy.Tenant, subnetID, op)
		exec(`INSERT INTO network_provider_bindings(tenant_id,subnet_id,resource_kind,binding_id,cluster_id,namespace,provider_name,provider_uid) VALUES($1,$2,'subnet',gen_random_uuid(),'test-cluster',$3,'old-entry','old-subnet-uid')`, legacy.Tenant, subnetID, ns)
		exec(`INSERT INTO network_load_balancers(tenant_id,lb_id,cluster_id,namespace,vpc_id,subnet_id,exposure,private_ip,state) VALUES($1,$2,'test-cluster',$3,$4,$5,'private','10.42.1.100','provisioning')`, legacy.Tenant, lbID, ns, legacy.VPC, subnetID)
		exec(`INSERT INTO network_lb_vip_intents(tenant_id,lb_id,cluster_id,namespace,vpc_id,subnet_id,address) VALUES($1,$2,'test-cluster',$3,$4,$5,'10.42.1.100')`, legacy.Tenant, lbID, ns, legacy.VPC, subnetID)
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		for _, table := range tables {
			var columnsValue string
			if err = owner.QueryRow(ctx, `SELECT string_agg(quote_ident(column_name),',' ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='public' AND table_name=$1`, table).Scan(&columnsValue); err != nil {
				t.Fatal(err)
			}
			columns[table] = columnsValue
			before[table] = snapshot(owner, table)
		}
	})
	for _, table := range tables {
		if got := snapshot(db.Owner, table); got != before[table] {
			t.Fatal("0007 changed old data", table)
		}
	}
	auth := biz.WithEgressCaller(ctx, biz.EgressCaller{TenantID: legacy.Tenant})
	lbs, err := biz.NewLoadBalancers(db.Repository, biz.ContextEgressAuthorization{}, []byte(strings.Repeat("c", 32)), time.Minute, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lbs.Get(auth, "", lbID); biz.ReasonOf(err) != biz.ResourceNotFound {
		t.Fatal("dormant U01 identity adopted as product", err)
	}
	items, _, total, err := lbs.List(auth, biz.ListLoadBalancers{ListVPCs: biz.ListVPCs{Limit: 20}})
	if err != nil || len(items) != 0 || total != 0 {
		t.Fatal("dormant U01 identity listed", items, err)
	}
	var occupied, tasks int
	if err = db.Owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM network_lb_vip_intents WHERE tenant_id=$1 AND lb_id=$2 AND released_at IS NULL),(SELECT count(*) FROM network_reconciliations WHERE tenant_id=$1 AND lb_id=$2)`, legacy.Tenant, lbID).Scan(&occupied, &tasks); err != nil || occupied != 1 || tasks != 0 {
		t.Fatal("upgrade lost reservation or invented work", occupied, tasks, err)
	}
	for _, table := range []string{"network_lb_listeners", "network_lb_configurations", "network_lb_members", "network_lb_configuration_members", "network_lb_subnet_refs", "network_lb_components", "network_lb_generated_resources", "network_lb_capabilities"} {
		var safe bool
		if err = db.Owner.QueryRow(ctx, `SELECT has_table_privilege($1,$2,'SELECT') AND has_table_privilege($1,$2,'INSERT') AND has_table_privilege($1,$2,'UPDATE') AND NOT has_table_privilege($1,$2,'DELETE')`, db.RuntimeRole, table).Scan(&safe); err != nil || !safe {
			t.Fatal("new runtime grant contract", table, err)
		}
	}
}
