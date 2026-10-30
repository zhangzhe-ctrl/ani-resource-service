package data_test

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	controlled "github.com/zhangzhe-ctrl/ani-resource-service/tests/net05a/provider"
)

// The platform publishes this preplanned whitelist. Candidate reads, admission
// and the actual Provider create path run without node probe documents.
func configureVPCPresetFixture(t *testing.T, f *egressFixture, _ *controlled.Server, _ string) {
	t.Helper()
	if err := f.p.ConfigureVPCCIDRPresets([]string{"10.42.0.0/16", "10.61.0.0/16", "10.62.0.0/16"}); err != nil {
		t.Fatal(err)
	}
}

func TestVPCCIDRPresetContendsWithPlatformPool(t *testing.T) {
	f, api, _, _ := newBaseKCFixture(t, nil)
	pool := intranetIntent("competing-pool", "10.61.2.0/24", "10.61.2.1")
	pool.Pool.DefaultVPCUID = api.Object("vpcs", "kcn-system", "kcn-cluster")["metadata"].(map[string]any)["uid"].(string)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		_, err := f.n.CreateVPC(f.ctx, biz.CreateVPC{TenantID: f.tenant, Name: "competing-vpc", CIDR: "10.61.0.0/16", IdempotencyKey: "competing-vpc"})
		results <- err
	}()
	go func() { defer group.Done(); <-start; _, err := f.e.CreatePlatform(f.ctx, pool); results <- err }()
	close(start)
	group.Wait()
	close(results)
	success, rejected := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if biz.ReasonOf(err) == biz.ResourceInUse {
			rejected++
		} else {
			t.Fatal("unexpected pool/VPC admission outcome", err)
		}
	}
	if success != 1 || rejected != 1 {
		t.Fatal("cluster lock allowed overlapping admissions", success, rejected)
	}
}

func TestVPCCIDRPresetsMainFlow(t *testing.T) {
	var nodeFactsReads atomic.Int64
	f, api, _, _ := newBaseKCFixture(t, func(w http.ResponseWriter, r *http.Request, _ *controlled.Server) bool {
		if r.Method == http.MethodGet && r.URL.Query().Get("labelSelector") == "network.ani.io/managed-by=ani-network-node-facts" {
			nodeFactsReads.Add(1)
			http.Error(w, "node probes are not deployed", http.StatusForbidden)
			return true
		}
		return false
	})
	values, err := f.n.ListVPCCIDRPresets(f.ctx, f.tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 || values[1] != "10.61.0.0/16" {
		t.Fatal("preplanned whitelist was not returned", values)
	}
	r := biz.CreateVPC{TenantID: f.tenant, Name: "preset-main", CIDR: values[1], IdempotencyKey: "preset-main"}
	v, err := f.n.CreateVPC(f.ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	// A changed whitelist affects new acceptance, not the durable receipt or
	// its first Provider create. No node probe document is present in the fixture.
	if err = f.p.ConfigureVPCCIDRPresets(nil); err != nil {
		t.Fatal(err)
	}
	replay, err := f.n.CreateVPC(f.ctx, r)
	if err != nil || replay.ID != v.ID {
		t.Fatal("accepted replay was invalidated by policy change", replay, err)
	}
	baseState(t, f, v.ID, biz.Available)
	if api.Object("vpcs", "tenant-"+f.tenant, strings.Replace(v.ID, "_", "-", 1)) == nil {
		t.Fatal("accepted VPC did not traverse the real Provider create")
	}
	configureVPCPresetFixture(t, f, api, "")
	s, err := f.n.CreateSubnet(f.ctx, biz.CreateSubnet{TenantID: f.tenant, VPCID: v.ID, Name: "preset-child", CIDR: "10.61.1.0/24", IdempotencyKey: "preset-child"})
	if err != nil {
		t.Fatal(err)
	}
	driveSubnet(t, f.n, f.w, s, biz.Available)
	// Isolated VPCs may reuse a preset, including in the same tenant.
	reused := r
	reused.Name = "reuse"
	reused.IdempotencyKey = "reuse"
	other, err := f.n.CreateVPC(f.ctx, reused)
	if err != nil {
		t.Fatal(err)
	}
	baseState(t, f, other.ID, biz.Available)
	if err = f.p.ConfigureVPCCIDRPresets(nil); err != nil {
		t.Fatal(err)
	}
	replay, err = f.n.CreateVPC(f.ctx, r)
	if err != nil || replay.ID != v.ID {
		t.Fatal("accepted replay was invalidated by policy change", replay, err)
	}
	fresh := r
	fresh.IdempotencyKey = "empty-policy"
	if _, err = f.n.CreateVPC(f.ctx, fresh); biz.ReasonOf(err) != biz.InvalidArgument {
		t.Fatal("empty policy admitted a new VPC", err)
	}
	if values, err = f.n.ListVPCCIDRPresets(f.ctx, f.tenant); err != nil || len(values) != 0 {
		t.Fatal("empty policy supplied candidates", values, err)
	}
	for _, id := range []string{s.ID} {
		if _, err = f.n.DeleteSubnet(f.ctx, f.tenant, id); err != nil {
			t.Fatal(err)
		}
		driveSubnet(t, f.n, f.w, s, biz.Deleted)
	}
	for _, id := range []string{v.ID, other.ID} {
		if _, err = f.n.DeleteVPC(f.ctx, f.tenant, id); err != nil {
			t.Fatal(err)
		}
		baseState(t, f, id, biz.Deleted)
	}
	if nodeFactsReads.Load() != 0 {
		t.Fatal("VPC flow still read node probe documents", nodeFactsReads.Load())
	}
}

func TestVPCCIDRPresetsKeepPoolConflictsAndWhitelistAdmission(t *testing.T) {
	f, api, _, _ := newBaseKCFixture(t, nil)
	input := biz.CreateVPC{TenantID: f.tenant, Name: "unlisted", CIDR: "10.63.0.0/16", IdempotencyKey: "unlisted"}
	if _, err := f.n.CreateVPC(f.ctx, input); biz.ReasonOf(err) != biz.InvalidArgument {
		t.Fatal("unlisted private CIDR was accepted", err)
	}
	pool := intranetIntent("reserved-pool", "10.61.2.0/24", "10.61.2.1")
	pool.Pool.DefaultVPCUID = api.Object("vpcs", "kcn-system", "kcn-cluster")["metadata"].(map[string]any)["uid"].(string)
	if _, err := f.e.CreatePlatform(f.ctx, pool); err != nil {
		t.Fatal(err)
	}
	values, err := f.n.ListVPCCIDRPresets(f.ctx, f.tenant)
	if err != nil || len(values) != 2 || slices.Contains(values, "10.61.0.0/16") {
		t.Fatal("accepted pool conflict supplied a VPC candidate", values, err)
	}
	input.Name, input.CIDR, input.IdempotencyKey = "pool-conflict", "10.61.0.0/16", "pool-conflict"
	if _, err := f.n.CreateVPC(f.ctx, input); biz.ReasonOf(err) != biz.ResourceInUse {
		t.Fatal("accepted pool conflict admitted a VPC", err)
	}
}
