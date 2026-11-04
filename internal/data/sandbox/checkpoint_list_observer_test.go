package sandbox

import (
	"context"
	"errors"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
	"testing"
	"time"
)

const readCheckpointID = "77777777-7777-4777-8777-777777777777"
const readCheckpointMutationID = "88888888-8888-4888-8888-888888888888"

func listObserverFixture(t *testing.T) (*fake.FakeDynamicClient, *KubernetesProvider, biz.Sandbox, biz.CheckpointSource) {
	t.Helper()
	old, p, s := providerFixture(t)
	ns, err := old.Tracker().Get(namespaceGVR, "", s.Snapshot.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	c := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{}, ns)
	p.client = c
	source := biz.CheckpointSource{ClusterID: s.Snapshot.ClusterID, Namespace: s.Snapshot.Namespace, NamespaceUID: s.Snapshot.NamespaceUID, PVCName: "workspace-" + s.ID, PVCUID: "pvc-uid", PVName: "pv", PVUID: "pv-uid", CSIDriver: "test.csi", VolumeHandle: "volume-handle", SnapshotClass: "delete-class"}
	snap := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "snapshot.storage.k8s.io/v1", "kind": "VolumeSnapshot", "metadata": map[string]any{"name": "sandbox-checkpoint-" + readCheckpointID, "namespace": s.Snapshot.Namespace, "uid": "snapshot-uid", "resourceVersion": "1", "labels": stringMap(labels(s)), "annotations": map[string]any{providerMutationID: readCheckpointMutationID}}, "spec": map[string]any{"source": map[string]any{"persistentVolumeClaimName": source.PVCName}, "volumeSnapshotClassName": source.SnapshotClass}, "status": map[string]any{"readyToUse": true, "boundVolumeSnapshotContentName": "content-name"}}}
	content := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "snapshot.storage.k8s.io/v1", "kind": "VolumeSnapshotContent", "metadata": map[string]any{"name": "content-name", "uid": "content-uid", "resourceVersion": "1"}, "spec": map[string]any{"source": map[string]any{"volumeHandle": source.VolumeHandle}, "driver": source.CSIDriver, "deletionPolicy": "Delete", "volumeSnapshotClassName": source.SnapshotClass, "volumeSnapshotRef": map[string]any{"name": "sandbox-checkpoint-" + readCheckpointID, "namespace": s.Snapshot.Namespace, "uid": "snapshot-uid"}}, "status": map[string]any{"readyToUse": true, "snapshotHandle": "snapshot-handle"}}}
	if err = c.Tracker().Add(snap); err != nil {
		t.Fatal(err)
	}
	if err = c.Tracker().Add(content); err != nil {
		t.Fatal(err)
	}
	return c, p, s, source
}
func ledgerCheckpoint(s biz.Sandbox, source biz.CheckpointSource) biz.Checkpoint {
	completed := time.Now()
	return biz.Checkpoint{TenantID: s.TenantID, SandboxID: s.ID, ID: readCheckpointID, State: "ready", Phase: "done", CompletedAt: &completed, SnapshotName: "sandbox-checkpoint-" + readCheckpointID, SnapshotUID: "snapshot-uid", ContentName: "content-name", ContentUID: "content-uid", SnapshotHandle: "snapshot-handle", SnapshotMutationID: readCheckpointMutationID, Source: source}
}
func TestCheckpointLedgerLiveObserverUsesRecordedUIDs(t *testing.T) {
	c, p, s, source := listObserverFixture(t)
	ctx := biz.WithCaller(context.Background(), biz.Caller{TenantID: s.TenantID, Actor: "test"})
	cp := ledgerCheckpoint(s, source)
	out, err := p.ObserveRecordedCheckpoint(ctx, s, cp)
	if err != nil || out.State != "ready" {
		t.Fatalf("observation=%+v err=%v", out, err)
	}
	cp.SnapshotHandle = "different-backend-snapshot"
	out, err = p.ObserveRecordedCheckpoint(ctx, s, cp)
	if err != nil || out.State != "identity_mismatch" {
		t.Fatalf("changed snapshot handle: observation=%+v err=%v", out, err)
	}
	cp = ledgerCheckpoint(s, source)
	cp.SnapshotUID = "replaced"
	out, err = p.ObserveRecordedCheckpoint(ctx, s, cp)
	if err != nil || out.State != "identity_mismatch" {
		t.Fatalf("observation=%+v err=%v", out, err)
	}
	cp = ledgerCheckpoint(s, source)
	if err = c.Resource(checkpointReadContentGVR).Delete(ctx, cp.ContentName, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	out, err = p.ObserveRecordedCheckpoint(ctx, s, cp)
	if err != nil || out.State != "missing" {
		t.Fatalf("observation=%+v err=%v", out, err)
	}
}
func TestCheckpointLedgerObserverRejectsNamespaceAndProviderFailure(t *testing.T) {
	c, p, s, source := listObserverFixture(t)
	ctx := biz.WithCaller(context.Background(), biz.Caller{TenantID: s.TenantID, Actor: "test"})
	cp := ledgerCheckpoint(s, source)
	cp.Source.NamespaceUID = "old"
	if _, err := p.ObserveRecordedCheckpoint(ctx, s, cp); !errors.Is(err, biz.ErrOwnership) {
		t.Fatalf("wrong namespace %v", err)
	}
	cp = ledgerCheckpoint(s, source)
	c.PrependReactor("get", "volumesnapshots", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(checkpointReadSnapshotGVR.GroupResource(), "", errors.New("forbidden"))
	})
	if _, err := p.ObserveRecordedCheckpoint(ctx, s, cp); !apierrors.IsForbidden(err) {
		t.Fatalf("provider failure %v", err)
	}
}
func TestCheckpointLedgerObserverDetectsChangingUID(t *testing.T) {
	c, p, s, source := listObserverFixture(t)
	ctx := biz.WithCaller(context.Background(), biz.Caller{TenantID: s.TenantID, Actor: "test"})
	cp := ledgerCheckpoint(s, source)
	reads := 0
	c.PrependReactor("get", "volumesnapshots", func(a ktesting.Action) (bool, runtime.Object, error) {
		reads++
		if reads == 1 {
			return false, nil, nil
		}
		o, e := c.Tracker().Get(checkpointReadSnapshotGVR, s.Snapshot.Namespace, cp.SnapshotName)
		if e != nil {
			return true, nil, e
		}
		copy := o.(*unstructured.Unstructured).DeepCopy()
		copy.SetUID(types.UID("replacement"))
		return true, copy, nil
	})
	if _, err := p.ObserveRecordedCheckpoint(ctx, s, cp); !errors.Is(err, biz.ErrDependencyUnavailable) {
		t.Fatalf("changed %v", err)
	}
}

func TestCheckpointLedgerObserverTreatsAnyCSIErrorAsNotReady(t *testing.T) {
	for _, tc := range []struct {
		name       string
		gvr        schema.GroupVersionResource
		namespaced bool
	}{
		{"snapshot", checkpointReadSnapshotGVR, true}, {"content", checkpointReadContentGVR, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, p, s, source := listObserverFixture(t)
			ctx := biz.WithCaller(context.Background(), biz.Caller{TenantID: s.TenantID, Actor: "test"})
			cp := ledgerCheckpoint(s, source)
			if tc.namespaced {
				api := c.Resource(tc.gvr).Namespace(s.Snapshot.Namespace)
				obj, err := api.Get(ctx, cp.SnapshotName, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				_ = unstructured.SetNestedField(obj.Object, map[string]any{"time": "2026-11-04T00:00:00Z"}, "status", "error")
				if _, err = api.Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			} else {
				api := c.Resource(tc.gvr)
				obj, err := api.Get(ctx, cp.ContentName, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				_ = unstructured.SetNestedField(obj.Object, map[string]any{"time": "2026-11-04T00:00:00Z"}, "status", "error")
				if _, err = api.Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			out, err := p.ObserveRecordedCheckpoint(ctx, s, cp)
			if err != nil || out.State != "not_ready" {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}
func TestCheckpointLedgerObserverRejectsMissingHandle(t *testing.T) {
	c, p, s, source := listObserverFixture(t)
	ctx := biz.WithCaller(context.Background(), biz.Caller{TenantID: s.TenantID, Actor: "test"})
	cp := ledgerCheckpoint(s, source)
	cp.SnapshotHandle = ""
	out, err := p.ObserveRecordedCheckpoint(ctx, s, cp)
	if err != nil || out.State != "identity_unknown" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	cp = ledgerCheckpoint(s, source)
	api := c.Resource(checkpointReadContentGVR)
	obj, err := api.Get(ctx, cp.ContentName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	unstructured.RemoveNestedField(obj.Object, "status", "snapshotHandle")
	if _, err = api.Update(ctx, obj, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	out, err = p.ObserveRecordedCheckpoint(ctx, s, cp)
	if err != nil || out.State != "not_ready" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}
