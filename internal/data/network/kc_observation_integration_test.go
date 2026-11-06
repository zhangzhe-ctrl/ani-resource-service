package data_test

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	data "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network"
	controlled "github.com/zhangzhe-ctrl/ani-resource-service/tests/net05a/provider"
	"k8s.io/client-go/rest"
)

func awaitNET05A(t *testing.T, timeout time.Duration, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not converge")
}

func TestObjectWatchQueuesDirectNativeStatusReads(t *testing.T) {
	p, _ := database(t)
	ctx := context.Background()
	api := controlled.New()
	host := httptest.NewServer(api)
	defer host.Close()
	provider, err := data.NewKCProvider(p, &rest.Config{Host: host.URL, QPS: 1000, Burst: 1000})
	if err != nil {
		t.Fatal(err)
	}
	policy := biz.DefaultWorkerPolicy()
	policy.ObserveEvery = 20 * time.Millisecond
	worker, err := biz.NewWorker(p, provider, uuid.NewString(), policy)
	if err != nil {
		t.Fatal(err)
	}
	observer, err := provider.EnableObservation(policy.RequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	live, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- observer.Start(live) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	n := newNetwork(t, p, time.Minute)
	tenant := uuid.NewString()
	namespace := "tenant-" + tenant
	api.Change("namespaces", map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace, "uid": uuid.NewString(), "labels": map[string]any{"network.ani.io/managed-by": "ani-network-service", "network.ani.io/tenant-id": tenant}}, "status": map[string]any{"phase": "Active"}}, false)
	v, err := n.CreateVPC(ctx, biz.CreateVPC{TenantID: tenant, Name: "watch", CIDR: "10.42.0.0/16", IdempotencyKey: "watch"})
	if err != nil {
		t.Fatal(err)
	}
	waitState := func(expected biz.ResourceState) {
		t.Helper()
		awaitNET05A(t, 3*time.Second, func() bool {
			if _, err := worker.Step(ctx); err != nil {
				t.Fatal(err)
			}
			v, err = n.GetVPC(ctx, tenant, v.ID)
			return err == nil && v.State == expected
		})
	}
	waitState(biz.Available)
	name := "vpc-" + v.ID[4:]
	changeReady := func(value string) {
		object := api.Object("vpcs", namespace, name)
		for _, raw := range object["status"].(map[string]any)["conditions"].([]any) {
			condition := raw.(map[string]any)
			if condition["type"] == "Ready" {
				condition["status"] = value
			}
		}
		api.Change("vpcs", object, false)
	}
	changeReady("False")
	waitState(biz.Degraded)
	changeReady("True")
	waitState(biz.Available)
	// Events only enqueue. Without a worker they cannot renew the projection.
	before := v
	changeReady("False")
	awaitNET05A(t, 3*time.Second, func() bool { return observer.Snapshot()["notifications_total"] > 0 })
	v, err = n.GetVPC(ctx, tenant, v.ID)
	if err != nil || v.State != before.State || v.Version != before.Version || !v.ObservedAt.Equal(*before.ObservedAt) || v.ObservationStale {
		t.Fatal("event changed an unapplied resource projection", v, err)
	}
	waitState(biz.Degraded)
}

// This verifies v3's task wake and direct-GET contract with a real isolated
// Postgres database and a controlled List/Watch provider. It is not live KCN.
func TestObserverStoppedAllowsManagedWorkAndRestartReconcilesMissedChange(t *testing.T) {
	p, owner := database(t)
	ctx := context.Background()
	api := controlled.New()
	host := httptest.NewServer(api)
	defer host.Close()
	config := &rest.Config{Host: host.URL, QPS: 1000, Burst: 1000}
	provider, err := data.NewKCProvider(p, config)
	if err != nil {
		t.Fatal(err)
	}
	policy := biz.DefaultWorkerPolicy()
	policy.ObserveEvery = 20 * time.Millisecond
	worker, err := biz.NewWorker(p, provider, uuid.NewString(), policy)
	if err != nil {
		t.Fatal(err)
	}
	startObserver := func(pr *data.KCProvider) (context.CancelFunc, <-chan error) {
		observer, err := pr.EnableObservation(policy.RequestTimeout)
		if err != nil {
			t.Fatal(err)
		}
		live, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- observer.Start(live) }()
		awaitNET05A(t, 5*time.Second, func() bool { return observer.Snapshot()["source_synced"] == 1 })
		return cancel, done
	}
	cancel, done := startObserver(provider)
	stopped := false
	defer func() {
		if !stopped {
			cancel()
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}()
	n := newNetwork(t, p, time.Minute)
	tenant := uuid.NewString()
	namespace := "tenant-" + tenant
	api.Change("namespaces", map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace, "uid": uuid.NewString(), "labels": map[string]any{"network.ani.io/managed-by": "ani-network-service", "network.ani.io/tenant-id": tenant}}, "status": map[string]any{"phase": "Active"}}, false)
	v, err := n.CreateVPC(ctx, biz.CreateVPC{TenantID: tenant, Name: "managed-original", CIDR: "10.42.0.0/16", IdempotencyKey: "managed-original"})
	if err != nil {
		t.Fatal(err)
	}
	waitState := func(id string, state biz.ResourceState) biz.VPC {
		t.Helper()
		var got biz.VPC
		awaitNET05A(t, 5*time.Second, func() bool {
			if _, err := worker.Step(ctx); err != nil {
				t.Fatal(err)
			}
			got, err = n.GetVPC(ctx, tenant, id)
			return err == nil && got.State == state
		})
		return got
	}
	before := waitState(v.ID, biz.Available)
	history, err := n.GetOperation(ctx, tenant, v.LastOperationID)
	if err != nil || history.State != biz.Succeeded {
		t.Fatal(history, err)
	}
	name := "vpc-" + v.ID[4:]
	original := api.Object("vpcs", namespace, name)
	originalUID := original["metadata"].(map[string]any)["uid"]
	if originalUID == nil || originalUID == "" {
		t.Fatal("provider identity missing")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	stopped = true
	// Drain notifications that were already persisted before observation
	// stopped, so only restart wake/relist can observe the missed event.
	awaitNET05A(t, 5*time.Second, func() bool {
		if _, err := worker.Step(ctx); err != nil {
			t.Fatal(err)
		}
		var parked bool
		err := owner.QueryRow(ctx, `SELECT requested_generation = processed_generation AND next_run_at = 'infinity'::timestamptz FROM network_reconciliations WHERE tenant_id=$1 AND vpc_id=$2`, tenant, v.ID).Scan(&parked)
		if err != nil {
			t.Fatal(err)
		}
		return parked
	})
	before, err = n.GetVPC(ctx, tenant, v.ID)
	if err != nil || before.State != biz.Available {
		t.Fatal(before, err)
	}
	for _, raw := range original["status"].(map[string]any)["conditions"].([]any) {
		condition := raw.(map[string]any)
		if condition["type"] == "Ready" {
			condition["status"] = "False"
		}
	}
	api.Change("vpcs", original, false)
	requestsBefore, _ := api.Counts()
	got, err := n.GetVPC(ctx, tenant, v.ID)
	requestsAfter, _ := api.Counts()
	if !reflect.DeepEqual(requestsBefore, requestsAfter) {
		t.Fatal("pure read contacted provider", requestsBefore, requestsAfter)
	}
	if err != nil || got.State != before.State || got.Version != before.Version || got.ObservationStale || !got.ObservedAt.Equal(*before.ObservedAt) {
		t.Fatalf("stopped observer changed persisted read: %+v %v", got, err)
	}
	// New managed intent remains admissible and worker GETs work with observation
	// stopped; v3 no longer gates business on the old freshness deadline.
	otherTenant := uuid.NewString()
	otherNS := "tenant-" + otherTenant
	api.Change("namespaces", map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": otherNS, "uid": uuid.NewString(), "labels": map[string]any{"network.ani.io/managed-by": "ani-network-service", "network.ani.io/tenant-id": otherTenant}}, "status": map[string]any{"phase": "Active"}}, false)
	other, err := n.CreateVPC(ctx, biz.CreateVPC{TenantID: otherTenant, Name: "while-stopped", CIDR: "10.42.0.0/16", IdempotencyKey: "while-stopped"})
	if err != nil {
		t.Fatalf("managed write while observer stopped: %v", err)
	}
	awaitNET05A(t, 5*time.Second, func() bool {
		if _, err := worker.Step(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := n.GetVPC(ctx, otherTenant, other.ID)
		return err == nil && got.State == biz.Available
	})
	// Prove the missed event remained unapplied even after unrelated managed
	// work; restart recovery must then wake or relist the task.
	got, err = n.GetVPC(ctx, tenant, v.ID)
	if err != nil || got.State != before.State || got.Version != before.Version || !got.ObservedAt.Equal(*before.ObservedAt) {
		t.Fatalf("original task progressed before restart: %+v %v", got, err)
	}
	restarted, err := data.NewKCProvider(p, config)
	if err != nil {
		t.Fatal(err)
	}
	cancel, done = startObserver(restarted)
	stopped = false
	after := waitState(v.ID, biz.Degraded)
	if after.ID != before.ID || after.TenantID != before.TenantID || api.Object("vpcs", namespace, name)["metadata"].(map[string]any)["uid"] != originalUID {
		t.Fatal("restart changed managed identity", before.ID, after.ID)
	}
	op, err := n.GetOperation(ctx, tenant, v.LastOperationID)
	if err != nil || !reflect.DeepEqual(history, op) {
		t.Fatalf("observation rewrote completed operation: %+v %v", op, err)
	}
}
