package sandbox

import (
	"context"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
)

func TestCheckpointWorkerRecoveryRequiresAttributedProviderFacts(t *testing.T) {
	_, p, s, src, class := checkpointProviderFixture(t)
	c := biz.Checkpoint{TenantID: s.TenantID, SandboxID: s.ID, ID: ckID, MutationID: ckMutation, MutationKind: "protect", Source: biz.CheckpointSource{ClusterID: s.Snapshot.ClusterID, Namespace: s.Snapshot.Namespace, NamespaceUID: src.NamespaceUID, PVCName: src.PVCName, PVCUID: src.PVCUID, PVName: src.PVName, PVUID: src.PVUID, CSIDriver: src.Driver, VolumeHandle: src.VolumeHandle, SnapshotClass: class.Name, SnapshotClassUID: class.UID, SnapshotClassResourceVersion: class.ResourceVersion}}
	m := biz.CheckpointMutation{ID: ckMutation, Kind: "protect"}
	got, err := p.ObserveCheckpointMutation(context.Background(), s, c, m)
	if err != nil || got.Outcome != biz.MutationUnknown {
		t.Fatalf("absent protect was considered complete: %#v %v", got, err)
	}
	if _, err = p.checkpointProtectPVC(context.Background(), s, src, c.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err = p.ObserveCheckpointMutation(context.Background(), s, c, m)
	if err != nil || got.Outcome != biz.MutationApplied || got.UID != src.PVCUID {
		t.Fatalf("protect not recovered: %#v %v", got, err)
	}
	c.MutationKind = "release"
	m.Kind = "release"
	got, err = p.ObserveCheckpointMutation(context.Background(), s, c, m)
	if err != nil || got.Outcome != biz.MutationUnknown {
		t.Fatalf("existing protection considered released: %#v %v", got, err)
	}
	if _, err = p.checkpointProtectPVC(context.Background(), s, src, c.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err = p.ObserveCheckpointMutation(context.Background(), s, c, m)
	if err != nil || got.Outcome != biz.MutationApplied || got.UID != src.PVCUID {
		t.Fatalf("release not recovered: %#v %v", got, err)
	}
	c.MutationKind = "create"
	m.Kind = "create"
	got, err = p.ObserveCheckpointMutation(context.Background(), s, c, m)
	if err != nil || got.Outcome != biz.MutationUnknown {
		t.Fatalf("absent snapshot considered created: %#v %v", got, err)
	}
}

func TestCheckpointProtectRejectsMutatingWebhookReceipt(t *testing.T) {
	c, p, s, src, _ := checkpointProviderFixture(t)
	pvc, err := c.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(context.Background(), src.PVCName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.PrependReactor("patch", "persistentvolumeclaims", func(ktesting.Action) (bool, runtime.Object, error) { return true, pvc.DeepCopy(), nil })
	got, err := p.checkpointProtectPVC(context.Background(), s, src, ckProtection, true)
	if err == nil || got.Outcome != biz.MutationUnknown {
		t.Fatalf("unchanged patch reply was accepted: %#v %v", got, err)
	}
}

func TestCheckpointReadyObservationSanitizesCSIError(t *testing.T) {
	client, p, s, src, class := checkpointProviderFixture(t)
	snapshotFixture(t, client, s, src)
	snapshot, err := client.Resource(checkpointSnapshotGVR).Namespace(s.Snapshot.Namespace).Get(context.Background(), "sandbox-checkpoint-"+ckID, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(snapshot.Object, map[string]any{"message": "sensitive backend credential"}, "status", "error"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Resource(checkpointSnapshotGVR).Namespace(s.Snapshot.Namespace).Update(context.Background(), snapshot, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	c := biz.Checkpoint{TenantID: s.TenantID, SandboxID: s.ID, ID: ckID, SnapshotMutationID: ckMutation, SnapshotUID: "snapshot-uid", Source: biz.CheckpointSource{NamespaceUID: src.NamespaceUID, PVCName: src.PVCName, PVCUID: src.PVCUID, PVName: src.PVName, PVUID: src.PVUID, CSIDriver: src.Driver, VolumeHandle: src.VolumeHandle, SnapshotClass: class.Name, SnapshotClassUID: class.UID, SnapshotClassResourceVersion: class.ResourceVersion}}
	got, err := p.ObserveCheckpointReady(context.Background(), s, c)
	if err != nil || got.Ready || got.Reason != "CSI_SNAPSHOT_LAST_ERROR" {
		t.Fatalf("observation = %#v, err = %v", got, err)
	}
}
