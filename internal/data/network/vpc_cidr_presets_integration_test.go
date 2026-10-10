package data_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	data "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network"
	controlled "github.com/zhangzhe-ctrl/ani-resource-service/tests/net05a/provider"
	"k8s.io/client-go/tools/clientcmd"
)

// Assigned/reserved platform facts live at the existing external KC protocol
// boundary. Admission and Provider rechecks use the actual policy and adapter.
func configureVPCPresetFixture(t *testing.T, f *egressFixture, api *controlled.Server, kube string) {
	t.Helper()
	nodeUID := uuid.NewString()
	api.Change("nodes", map[string]any{"apiVersion": "v1", "kind": "Node", "metadata": map[string]any{"name": "node-1", "uid": nodeUID}, "status": map[string]any{"addresses": []any{map[string]any{"type": "InternalIP", "address": "172.16.101.10"}}}}, false)
	doc := data.NodeFactsDocument{Version: 1, NodeName: "node-1", NodeUID: nodeUID, CollectedAt: time.Now(), Interfaces: []biz.NodeInterface{{NodeName: "node-1", NodeUID: nodeUID, Name: "eth0", Kind: "device", Management: true, Addresses: []string{"172.16.101.10/24"}}}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	api.Change("configmaps", map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"namespace": "kcn-system", "name": "node-facts-node-1", "uid": uuid.NewString(), "labels": map[string]any{"network.ani.io/managed-by": "ani-network-node-facts", "network.ani.io/node-uid": nodeUID}}, "data": map[string]any{"facts.json": string(raw)}}, false)
	api.Change("subnets", map[string]any{"apiVersion": "networking.kubercloud.com/v1", "kind": "Subnet", "metadata": map[string]any{"namespace": "kcn-system", "name": "kcn-default", "uid": uuid.NewString()}, "spec": map[string]any{"type": "VPC", "cidrBlock": "10.16.0.0/16", "gateway": "kcn-cluster"}}, false)
	cfg, err := clientcmd.BuildConfigFromFlags("", kube)
	if err != nil {
		t.Fatal(err)
	}
	cfg.QPS, cfg.Burst = 1000, 1000
	provider, err := data.NewKCProvider(f.p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.p.ConfigureVPCCIDRPresets([]string{"10.42.0.0/16", "10.61.0.0/16", "10.62.0.0/16", "10.96.0.0/16", "172.16.101.0/24"}, provider); err != nil {
		t.Fatal(err)
	}
}

func TestVPCCIDRPresetAcceptsFactsRenewedDuringRead(t *testing.T) {
	var armed atomic.Bool
	f, _, _, _ := newBaseKCFixture(t, func(_ http.ResponseWriter, r *http.Request, api *controlled.Server) bool {
		if armed.Load() && r.Method == "GET" && r.URL.Path == "/api/v1/configmaps" {
			cm := api.Object("configmaps", "kcn-system", "node-facts-node-1")
			var doc data.NodeFactsDocument
			if err := json.Unmarshal([]byte(cm["data"].(map[string]any)["facts.json"].(string)), &doc); err != nil {
				t.Error(err)
				return false
			}
			doc.CollectedAt = time.Now()
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Error(err)
				return false
			}
			cm["data"].(map[string]any)["facts.json"] = string(raw)
			api.Change("configmaps", cm, false)
		}
		return false
	})
	armed.Store(true)
	values, err := f.n.ListVPCCIDRPresets(f.ctx, f.tenant)
	if err != nil || len(values) != 3 {
		t.Fatal("normal renewal was treated as future facts", values, err)
	}
}

func TestVPCCIDRPresetUnknownAndExpiredFacts(t *testing.T) {
	f, api, _, _ := newBaseKCFixture(t, nil)
	r := biz.CreateVPC{TenantID: f.tenant, Name: "guard", CIDR: "10.61.0.0/16", IdempotencyKey: "guard"}
	cm := api.Object("configmaps", "kcn-system", "node-facts-node-1")
	saved := cm["data"].(map[string]any)["facts.json"].(string)
	var doc data.NodeFactsDocument
	if err := json.Unmarshal([]byte(saved), &doc); err != nil {
		t.Fatal(err)
	}
	doc.CollectedAt = time.Now().Add(-2 * time.Minute)
	raw, _ := json.Marshal(doc)
	cm["data"].(map[string]any)["facts.json"] = string(raw)
	api.Change("configmaps", cm, false)
	if _, err := f.n.ListVPCCIDRPresets(f.ctx, f.tenant); biz.ReasonOf(err) != biz.DependencyUnavailable || errors.Unwrap(err) == nil {
		t.Fatal("expired facts supplied candidates", err)
	}
	if _, err := f.n.CreateVPC(f.ctx, r); biz.ReasonOf(err) != biz.DependencyUnavailable {
		t.Fatal("expired facts admitted VPC", err)
	}
	api.Change("configmaps", cm, true)
	if _, err := f.n.CreateVPC(f.ctx, r); biz.ReasonOf(err) != biz.DependencyUnavailable {
		t.Fatal("missing NodeFacts admitted VPC", err)
	}
	cm["data"].(map[string]any)["facts.json"] = saved
	api.Change("configmaps", cm, false)
	accepted, err := f.n.CreateVPC(f.ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	// A platform network appeared after SQL admission, before the first POST.
	conflict := map[string]any{"apiVersion": "networking.kubercloud.com/v1", "kind": "Subnet", "metadata": map[string]any{"namespace": "kcn-system", "name": "reserved-after-admission", "uid": uuid.NewString()}, "spec": map[string]any{"type": "System", "cidrBlock": "10.61.2.0/24"}}
	api.Change("subnets", conflict, false)
	f.drive(t, func() bool { v, e := f.n.GetVPC(f.ctx, f.tenant, accepted.ID); return e == nil && v.Reason != "" })
	if api.Object("vpcs", "tenant-"+f.tenant, strings.Replace(accepted.ID, "_", "-", 1)) != nil {
		t.Fatal("Provider created VPC after platform conflict appeared")
	}
	api.Change("subnets", conflict, true)
	baseState(t, f, accepted.ID, biz.Available)
	if _, err = f.n.DeleteVPC(f.ctx, f.tenant, accepted.ID); err != nil {
		t.Fatal(err)
	}
	baseState(t, f, accepted.ID, biz.Deleted)
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
	f, _, _, _ := newBaseKCFixture(t, nil)
	values, err := f.n.ListVPCCIDRPresets(f.ctx, f.tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 || values[1] != "10.61.0.0/16" {
		t.Fatal("platform conflicts were not filtered", values)
	}
	r := biz.CreateVPC{TenantID: f.tenant, Name: "preset-main", CIDR: values[1], IdempotencyKey: "preset-main"}
	v, err := f.n.CreateVPC(f.ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	baseState(t, f, v.ID, biz.Available)
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
	if err = f.p.ConfigureVPCCIDRPresets(nil, nil); err != nil {
		t.Fatal(err)
	}
	replay, err := f.n.CreateVPC(f.ctx, r)
	if err != nil || replay.ID != v.ID {
		t.Fatal("accepted replay was invalidated by policy change", replay, err)
	}
	fresh := r
	fresh.IdempotencyKey = "empty-policy"
	if _, err = f.n.CreateVPC(f.ctx, fresh); biz.ReasonOf(err) != biz.InvalidArgument {
		t.Fatal("empty policy admitted a new VPC", err)
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
}
