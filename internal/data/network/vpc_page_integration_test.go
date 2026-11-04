package data_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	networkv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	service "github.com/zhangzhe-ctrl/ani-resource-service/internal/service/network"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestVPCPageRandomAccessAndScope(t *testing.T) {
	repo, owner := database(t)
	n := newNetwork(t, repo, time.Minute)
	s := service.NewNetworkService(n)
	ctx := context.Background()
	tenant, other := uuid.NewString(), uuid.NewString()
	configureExistingTenantNamespaces(t, repo, tenant, other)
	ids := []string{}
	for i := 0; i < 7; i++ {
		v, err := n.CreateVPC(ctx, biz.CreateVPC{TenantID: tenant, Name: "page-match", CIDR: "10.42.0.0/16", IdempotencyKey: fmt.Sprint("page-", i)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.ID)
	}
	for _, tid := range []string{tenant, other} {
		if _, err := n.CreateVPC(ctx, biz.CreateVPC{TenantID: tid, Name: "excluded", CIDR: "10.42.0.0/16", IdempotencyKey: "excluded"}); err != nil {
			t.Fatal(err)
		}
	}
	// Force equal primary sort keys to exercise the unique-key tie breaker.
	if _, err := owner.Exec(ctx, `UPDATE network_vpcs SET created_at='2026-10-01T00:00:00Z' WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	query := func(page uint32) *networkv1.ListVPCsResponse {
		t.Helper()
		r, err := s.ListVPCs(ctx, &networkv1.ListVPCsRequest{TenantId: tenant, Name: "page-match", Page: proto.Uint32(page), PageSize: proto.Uint32(2)})
		if err != nil {
			t.Fatal(err)
		}
		listTotal(t, r, 7)
		if r.NextCursor != "" {
			t.Fatal("page response contains cursor")
		}
		return r
	}
	// The first request is page three, without obtaining any continuation token.
	third := query(3)
	if len(third.Items) != 2 || third.Items[0].Id != ids[4] || third.Items[1].Id != ids[5] {
		t.Fatalf("third=%v want=%v", third.Items, ids[4:6])
	}
	if again := query(3); again.Items[0].Id != ids[4] {
		t.Fatal("unstable order")
	}
	if first := query(1); first.Items[0].Id != ids[0] || first.Items[1].Id != ids[1] {
		t.Fatal("first page mismatch")
	}
	if last := query(4); len(last.Items) != 1 || last.Items[0].Id != ids[6] {
		t.Fatal("last page mismatch")
	}
	if beyond := query(5); beyond.Items == nil || len(beyond.Items) != 0 {
		t.Fatal("out of range page must be an empty array")
	}
	for _, tc := range []struct {
		tid, name string
		state     networkv1.ResourceState
		total     int64
	}{{tenant, "missing", 0, 0}, {other, "page-match", 0, 0}, {other, "excluded", 0, 1}, {tenant, "page-match", networkv1.ResourceState_RESOURCE_STATE_AVAILABLE, 0}} {
		r, err := s.ListVPCs(ctx, &networkv1.ListVPCsRequest{TenantId: tc.tid, Name: tc.name, State: tc.state, PageSize: proto.Uint32(2)})
		if err != nil {
			t.Fatal(err)
		}
		listTotal(t, r, tc.total)
		if tc.total == 0 && (r.Items == nil || len(r.Items) != 0) {
			t.Fatal("empty page mismatch")
		}
	}
	for _, req := range []*networkv1.ListVPCsRequest{{TenantId: tenant, Page: proto.Uint32(0)}, {TenantId: tenant, PageSize: proto.Uint32(101)}, {TenantId: tenant, Page: proto.Uint32(1), Limit: 1}, {TenantId: tenant, Page: proto.Uint32(1), Cursor: "old"}} {
		if _, err := s.ListVPCs(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("want invalid: %v", err)
		}
	}
}

func TestVPCPageCountAndListShareSnapshot(t *testing.T) {
	repo, owner := database(t)
	n := newNetwork(t, repo, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tenant := uuid.NewString()
	configureExistingTenantNamespaces(t, repo, tenant)
	v, err := n.CreateVPC(ctx, biz.CreateVPC{TenantID: tenant, Name: "snapshot", CIDR: "10.42.0.0/16", IdempotencyKey: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err = lock.Exec(ctx, "LOCK TABLE network_vpc_base_connectivity IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		p   biz.VPCPage
		err error
	}
	done := make(chan result, 1)
	go func() {
		p, e := n.ListVPCs(ctx, biz.ListVPCs{TenantID: tenant, Name: "snapshot", Page: proto.Uint32(1)})
		done <- result{p, e}
	}()
	// Count reads only VPCs. The following List then waits on the joined table.
	for {
		var waiting bool
		err = owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE relation='network_vpc_base_connectivity'::regclass AND NOT granted)`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("query did not wait: %v", r.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err = owner.Exec(ctx, `UPDATE network_vpcs SET name='changed' WHERE tenant_id=$1 AND vpc_id=$2`, tenant, v.ID); err != nil {
		t.Fatal(err)
	}
	if err = lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.p.Total != 1 || len(r.p.Items) != 1 || r.p.Items[0].Name != "snapshot" {
		t.Fatalf("inconsistent snapshot: %+v %v", r.p, r.err)
	}
	next, err := n.ListVPCs(ctx, biz.ListVPCs{TenantID: tenant, Name: "snapshot", Page: proto.Uint32(1)})
	if err != nil || next.Total != 0 || len(next.Items) != 0 {
		t.Fatal("new request must see committed change", err)
	}
}
