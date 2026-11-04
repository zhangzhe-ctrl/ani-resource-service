package sandbox

import (
	"context"
	"errors"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
	"testing"
	"time"
)

func checkpointCleanupFixture(t *testing.T) (*KubernetesProvider, biz.Sandbox, biz.Checkpoint) {
	client, p, s, source, _ := checkpointProviderFixture(t)
	snapshotFixture(t, client, s, source)
	now := time.Now()
	c := biz.Checkpoint{TenantID: s.TenantID, SandboxID: s.ID, ID: ckID, State: "ready", Phase: "done", CompletedAt: &now, SnapshotMutationID: ckMutation, SnapshotName: "sandbox-checkpoint-" + ckID, SnapshotUID: "snapshot-uid", ContentName: "content-name", ContentUID: "content-uid", SnapshotHandle: "underlying-snapshot-id", Source: biz.CheckpointSource{ClusterID: s.Snapshot.ClusterID, Namespace: s.Snapshot.Namespace, NamespaceUID: source.NamespaceUID, PVCName: source.PVCName, PVCUID: source.PVCUID, PVName: source.PVName, PVUID: source.PVUID, CSIDriver: source.Driver, VolumeHandle: source.VolumeHandle, SnapshotClass: "delete-class", SnapshotClassUID: "class-uid", SnapshotClassResourceVersion: "1"}}
	return p, s, c
}
func TestCheckpointCleanupRequiresUIDAndContentReceipt(t *testing.T) {
	p, s, c := checkpointCleanupFixture(t)
	ctx := context.Background()
	receipt, err := p.checkpointDeleteSnapshot(ctx, s, c)
	if err != nil || receipt.Outcome != biz.MutationApplied {
		t.Fatalf("delete=%+v err=%v", receipt, err)
	}
	deleted := false
	for _, a := range p.client.(*fake.FakeDynamicClient).Actions() {
		if a.GetVerb() == "delete" && a.GetResource().Resource == "volumesnapshots" {
			opts := a.(ktesting.DeleteAction).GetDeleteOptions()
			if opts.Preconditions == nil || opts.Preconditions.UID == nil || string(*opts.Preconditions.UID) != c.SnapshotUID {
				t.Fatal("missing UID precondition")
			}
			deleted = true
		}
	}
	if !deleted {
		t.Fatal("missing snapshot delete")
	}
	done, err := p.checkpointCleanupObserved(ctx, s, c)
	if err != nil || done {
		t.Fatalf("content still present done=%v err=%v", done, err)
	}
	if err = p.client.Resource(checkpointContentGVR).Delete(ctx, c.ContentName, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	done, err = p.checkpointCleanupObserved(ctx, s, c)
	if err != nil || !done {
		t.Fatalf("cleanup=%v err=%v", done, err)
	}
}
func TestCheckpointCleanupUnknownDoesNotPretendDone(t *testing.T) {
	p, s, c := checkpointCleanupFixture(t)
	client := p.client.(*fake.FakeDynamicClient)
	client.PrependReactor("delete", "volumesnapshots", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, io.ErrUnexpectedEOF })
	receipt, err := p.checkpointDeleteSnapshot(context.Background(), s, c)
	if !errors.Is(err, io.ErrUnexpectedEOF) || receipt.Outcome != biz.MutationUnknown {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	done, err := p.checkpointCleanupObserved(context.Background(), s, c)
	if err != nil || done {
		t.Fatalf("unknown done=%v err=%v", done, err)
	}
}

func TestCheckpointCleanupRejectsForeignIdentity(t *testing.T) {
	p, s, c := checkpointCleanupFixture(t)
	c.SandboxID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	receipt, err := p.checkpointDeleteSnapshot(context.Background(), s, c)
	if !errors.Is(err, biz.ErrOwnership) || receipt.Outcome != biz.MutationNotSent {
		t.Fatalf("foreign checkpoint accepted: %+v %v", receipt, err)
	}
	for _, a := range p.client.(*fake.FakeDynamicClient).Actions() {
		if a.GetVerb() == "delete" && a.GetResource().Resource == "volumesnapshots" {
			t.Fatal("sent a delete for another Sandbox")
		}
	}
}

func TestCheckpointCleanupAfterWorkspaceSwitchAndTemporaryNotReady(t *testing.T) {
	p, s, c := checkpointCleanupFixture(t)
	// The checkpoint refers to the original source, while recovery has already
	// switched this Sandbox onto a different current workspace generation.
	s.ProviderRefs.PVCName = "workspace-new-generation"
	s.ProviderRefs.PVCUID = "new-pvc-uid"
	s.ProviderRefs.PVName = "new-pv"
	s.ProviderRefs.PVUID = "new-pv-uid"
	snap, err := p.client.Resource(checkpointSnapshotGVR).Namespace(s.Snapshot.Namespace).Get(context.Background(), c.SnapshotName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = unstructured.SetNestedField(snap.Object, false, "status", "readyToUse"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.client.Resource(checkpointSnapshotGVR).Namespace(s.Snapshot.Namespace).Update(context.Background(), snap, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	receipt, err := p.checkpointDeleteSnapshot(context.Background(), s, c)
	if err != nil || receipt.Outcome != biz.MutationApplied {
		t.Fatalf("old checkpoint cleanup=%+v err=%v", receipt, err)
	}
}
