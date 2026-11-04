package data_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/network"
	data "github.com/zhangzhe-ctrl/ani-resource-service/internal/data/network"
	controlled "github.com/zhangzhe-ctrl/ani-resource-service/tests/net05a/provider"
	"k8s.io/client-go/rest"
)

func TestManagedAttachmentPGFullStopIgnoresSiblingServiceRenewals(t *testing.T) {
	f := newAttachmentFixture(t)
	f.r.WorkloadMode = "controller_managed"
	f.r.ConsumerType = biz.ConsumerContainerInstance
	a := f.prepare(t)
	c := managedConsumerFor(a, "pod-1", 1)
	f.api.Mu.Lock()
	managedControllerObjects(f, a, c)
	f.api.Objects["pods/"+a.Namespace+"/notebook-0"] = managedPodObject(a, "pod-1")
	managedNetworkObjects(f, a, "pod-1")
	f.api.Mu.Unlock()
	setManagedConsumer(f, c)
	a = f.step(t, a.ID)
	if a.State != biz.Attached || a.PodUID != "pod-1" {
		t.Fatal("initial real worker did not attach", a)
	}
	var err error
	a, err = f.a.Confirm(context.Background(), biz.ConfirmAttachment{TenantID: a.TenantID, AttachmentID: a.ID, ClusterID: a.ClusterID, Namespace: a.Namespace, PodName: "notebook-0", PodUID: "pod-1", ExpectedVersion: a.Version, ExpectedSnapshotRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	a = f.step(t, a.ID)
	if a.State != biz.Attached || a.PodUID != "pod-1" {
		t.Fatal("confirmed worker did not restore current attachment", a)
	}
	before := a.ObservedAt

	// Kubeflow owns a Service through the same Notebook UID. That sibling's
	// renewal is not a Pod/VNic/VNicIP/EIP identity or address fact.
	service := map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": "notebook", "namespace": a.Namespace, "uid": "sibling-service", "ownerReferences": []any{map[string]any{"apiVersion": "kubeflow.org/v1", "kind": "Notebook", "name": "notebook", "uid": "notebook-uid", "controller": true}}}, "spec": map[string]any{"clusterIP": "10.96.0.20"}}
	ctl := controlled.New()
	ctl.Backend = f.api
	ctl.Change("services", service, false)
	f.api.Mu.Lock()
	for _, key := range []string{"pods/" + a.Namespace + "/notebook-0", "vnics/" + a.Namespace + "/nic", "vnicips/" + a.Namespace + "/current-ip"} {
		delete(f.api.Objects, key)
	}
	f.api.Mu.Unlock()
	old := c.CurrentPods[0]
	c.SnapshotRevision = 2
	c.DesiredRunState = "stopped"
	c.CurrentPods = nil
	c.RetiredPods = []biz.PodReference{old}
	setManagedConsumer(f, c)

	var observation atomic.Pointer[data.KCObservation]
	var renewals atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/eips") || r.URL.Query().Get("watch") == "true" || r.URL.Query().Get("resourceVersion") != "" {
			ctl.ServeHTTP(w, r)
			return
		}
		// Complete the external LIST response, then deliver a real Service Watch
		// update before this independent audit can finish. Repeat for every audit,
		// so extending the caller deadline cannot hide the incorrect scope.
		response := httptest.NewRecorder()
		ctl.ServeHTTP(response, r)
		observer := observation.Load()
		prior := observer.Snapshot()["coalesced_total"]
		next := ctl.Object("services", a.Namespace, "notebook")
		next["metadata"].(map[string]any)["annotations"] = map[string]any{"renewal": strconv.Itoa(int(renewals.Add(1)))}
		ctl.Change("services", next, false)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for observer.Snapshot()["coalesced_total"] <= prior {
			select {
			case <-ticker.C:
			case <-r.Context().Done():
				return
			}
		}
		for key, values := range response.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
	}))
	defer host.Close()
	provider, err := data.NewKCProvider(f.p, &rest.Config{Host: host.URL, QPS: 1000, Burst: 1000})
	if err != nil {
		t.Fatal(err)
	}
	options := data.DefaultObservationOptions()
	options.AuditInterval = time.Hour
	options.AuditJitter = time.Millisecond
	options.FlushInterval = 10 * time.Millisecond
	observer, err := provider.EnableObservation(options)
	if err != nil {
		t.Fatal(err)
	}
	observation.Store(observer)
	live, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- observer.Start(live) }()
	defer func() { cancel(); <-done }()
	awaitNET05A(t, 3*time.Second, func() bool {
		metrics := observer.Snapshot()
		_, published := metrics["audit_evidence_age_seconds"]
		return metrics["source_synced"] == 1 && published
	})
	// A first LIST can finish before Watch initial synchronization and is then
	// invalidated by the Watch continuity boundary. Request independent evidence
	// after synchronization; the sibling Service renewal is still injected into
	// every full audit and must not invalidate attachment relationship facts.
	previousRenewals := renewals.Load()
	auditCtx, auditCancel := context.WithTimeout(context.Background(), 3*time.Second)
	work := biz.AttachmentWork{Attachment: a, Plan: *a.Plan, ConsumerSnapshot: &c, ConsumerPodUIDs: []string{"pod-1"}, RequireFreshRelations: true, Relations: []byte("[]")}
	_, err = provider.ObserveAttachment(auditCtx, work)
	auditCancel()
	if err != nil {
		t.Fatal("post-sync complete attachment audit", err)
	}
	if renewals.Load() <= previousRenewals {
		t.Fatal("post-sync attachment audit did not deliver a sibling Service renewal")
	}
	policy := biz.DefaultWorkerPolicy()
	policy.RequestTimeout = 2 * time.Second
	policy.Lease = 8 * time.Second
	policy.ObserveEvery = 5 * time.Millisecond
	policy.RetryMin = time.Millisecond
	f.worker, err = biz.NewAttachmentWorker(f.p, provider, f.consumer, uuid.NewString(), policy)
	if err != nil {
		t.Fatal(err)
	}
	awaitNET05A(t, 3*time.Second, func() bool {
		worked, stepErr := f.worker.Step(context.Background())
		if stepErr != nil {
			t.Fatal(stepErr)
		}
		return worked
	})
	a, err = f.a.Get(context.Background(), f.r.TenantID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if renewals.Load() == 0 {
		t.Fatal("test did not deliver the sibling Service Watch update")
	}
	if a.State != biz.Reserved || a.Reason != "" || a.PodUID != "" || a.SnapshotRevision != 2 || a.ObservedAt == nil || before == nil || !a.ObservedAt.After(*before) {
		t.Fatalf("full Stop was starved by sibling Service renewals: state=%s reason=%s pod=%s revision=%d renewals=%d", a.State, a.Reason, a.PodUID, a.SnapshotRevision, renewals.Load())
	}
	var raw []byte
	if err = f.owner.QueryRow(context.Background(), `SELECT provider_relations FROM network_attachments WHERE tenant_id=$1 AND attachment_id=$2`, a.TenantID, a.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var relations []struct{ Current, AddressEligible bool }
	if err = json.Unmarshal(raw, &relations); err != nil {
		t.Fatal(err)
	}
	for _, relation := range relations {
		if relation.Current || relation.AddressEligible {
			t.Fatal("Stop retained a current relation or eligible address")
		}
	}
}
