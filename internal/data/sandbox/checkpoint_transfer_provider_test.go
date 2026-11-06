package sandbox

import (
	"context"
	"errors"
	"io"
	"testing"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

func transferProviderFixture(t *testing.T, mode string) (*fake.FakeDynamicClient, *KubernetesProvider, biz.Sandbox, biz.CheckpointTransfer) {
	t.Helper()
	c, p, s, source, _ := checkpointProviderFixture(t)
	snapshotFixture(t, c, s, source)
	class := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "storage.k8s.io/v1", "kind": "StorageClass", "metadata": map[string]any{"name": "fast", "uid": "storage-class-uid", "resourceVersion": "1"}, "provisioner": "test.csi", "reclaimPolicy": "Delete", "volumeBindingMode": mode}}
	if err := c.Tracker().Add(class); err != nil {
		t.Fatal(err)
	}
	c.PrependReactor("create", "persistentvolumeclaims", func(action ktesting.Action) (bool, runtime.Object, error) {
		obj := action.(ktesting.CreateAction).GetObject().(*unstructured.Unstructured)
		obj.SetUID(types.UID("candidate-pvc-uid"))
		obj.SetResourceVersion("1")
		return false, nil, nil
	})
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	candidateName, err := biz.CheckpointWorkspaceName(s.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	x := biz.CheckpointTransfer{TenantID: s.TenantID, ID: id, CheckpointID: ckID, SourceSandboxID: s.ID, TargetSandboxID: s.ID, SourceSnapshotName: "sandbox-checkpoint-" + ckID, SourceSnapshotUID: "snapshot-uid", SourceContentUID: "content-uid", SourceSnapshotHandle: "underlying-snapshot-id", CandidateName: candidateName, MutationID: ckMutation}
	return c, p, s, x
}
func bindTransferCandidate(t *testing.T, c *fake.FakeDynamicClient, s biz.Sandbox, x biz.CheckpointTransfer) {
	t.Helper()
	ctx := context.Background()
	pvc, err := c.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(ctx, x.CandidateName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = unstructured.SetNestedField(pvc.Object, "candidate-pv", "spec", "volumeName")
	_ = unstructured.SetNestedField(pvc.Object, "Bound", "status", "phase")
	if _, err = c.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Update(ctx, pvc, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	pv := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "PersistentVolume", "metadata": map[string]any{"name": "candidate-pv", "uid": "candidate-pv-uid", "resourceVersion": "1"}, "spec": map[string]any{"claimRef": map[string]any{"namespace": s.Snapshot.Namespace, "name": x.CandidateName, "uid": "candidate-pvc-uid"}, "storageClassName": "fast", "persistentVolumeReclaimPolicy": "Delete", "csi": map[string]any{"driver": "test.csi", "volumeHandle": "new-volume-handle"}}, "status": map[string]any{"phase": "Bound"}}}
	if err = c.Tracker().Add(pv); err != nil {
		t.Fatal(err)
	}
}
func TestCheckpointTransferWFFCRejectedBeforePVCWrite(t *testing.T) {
	c, p, s, x := transferProviderFixture(t, "WaitForFirstConsumer")
	if err := p.ValidateCheckpointTransferStorage(context.Background(), s.Snapshot.Workspace.StorageClass); !errors.Is(err, biz.ErrCheckpointWFFCUnsupported) {
		t.Fatalf("preflight: %v", err)
	}
	outcome, uid, err := p.CreateCheckpointTransferPVC(context.Background(), s, x, x.MutationID)
	if !errors.Is(err, biz.ErrCheckpointWFFCUnsupported) || outcome != biz.MutationNotSent || uid != "" {
		t.Fatalf("create %s %s %v", outcome, uid, err)
	}
	for _, action := range c.Actions() {
		if action.GetVerb() == "create" && action.GetResource().Resource == "persistentvolumeclaims" {
			t.Fatal("WFFC PVC create was sent")
		}
	}
}
func TestCheckpointTransferImmediateStillCreatesAndBinds(t *testing.T) {
	c, p, s, x := transferProviderFixture(t, "Immediate")
	if err := p.ValidateCheckpointTransferStorage(context.Background(), s.Snapshot.Workspace.StorageClass); err != nil {
		t.Fatal(err)
	}
	outcome, uid, err := p.CreateCheckpointTransferPVC(context.Background(), s, x, x.MutationID)
	if err != nil || outcome != biz.MutationApplied {
		t.Fatalf("create %s %v", outcome, err)
	}
	x.CandidateUID = uid
	bindTransferCandidate(t, c, s, x)
	obs, err := p.ObserveCheckpointTransferPVC(context.Background(), s, x)
	if err != nil || !obs.Bound || obs.PVUID != "candidate-pv-uid" {
		t.Fatalf("bound %#v %v", obs, err)
	}
}
func TestCheckpointTransferNeverAdoptsChangedIdentity(t *testing.T) {
	for _, name := range []string{"snapshot_uid", "content_uid", "datasource", "mutation", "pv_claim", "same_source_volume"} {
		t.Run(name, func(t *testing.T) {
			c, p, s, x := transferProviderFixture(t, "Immediate")
			_, uid, err := p.CreateCheckpointTransferPVC(context.Background(), s, x, x.MutationID)
			if err != nil {
				t.Fatal(err)
			}
			x.CandidateUID = uid
			bindTransferCandidate(t, c, s, x)
			switch name {
			case "snapshot_uid":
				x.SourceSnapshotUID = "different"
			case "content_uid":
				x.SourceContentUID = "different"
			case "datasource", "mutation":
				pvc, err := c.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(context.Background(), x.CandidateName, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if name == "datasource" {
					_ = unstructured.SetNestedField(pvc.Object, "other", "spec", "dataSource", "name")
				} else {
					pvc.SetAnnotations(map[string]string{providerMutationID: "other"})
				}
				if _, err = c.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Update(context.Background(), pvc, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			case "pv_claim", "same_source_volume":
				pv, err := c.Resource(pvGVR).Get(context.Background(), "candidate-pv", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if name == "pv_claim" {
					_ = unstructured.SetNestedField(pv.Object, "wrong-uid", "spec", "claimRef", "uid")
				} else {
					_ = unstructured.SetNestedField(pv.Object, "volume-handle", "spec", "csi", "volumeHandle")
				}
				if _, err = c.Resource(pvGVR).Update(context.Background(), pv, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			obs, err := p.ObserveCheckpointTransferPVC(context.Background(), s, x)
			if err == nil || obs.Bound {
				t.Fatalf("accepted changed identity: %#v %v", obs, err)
			}
		})
	}
}
func TestCheckpointTransferPVCUnknownAndMissing(t *testing.T) {
	c, p, s, x := transferProviderFixture(t, "Immediate")
	obs, err := p.ObserveCheckpointTransferPVC(context.Background(), s, x)
	if err != nil || obs.Exists {
		t.Fatalf("missing: %#v %v", obs, err)
	}
	c.PrependReactor("create", "persistentvolumeclaims", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, io.ErrUnexpectedEOF })
	outcome, _, err := p.CreateCheckpointTransferPVC(context.Background(), s, x, x.MutationID)
	if outcome != biz.MutationUnknown || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("timeout: %s %v", outcome, err)
	}
}

func TestCheckpointTransferGenerationPodMatchesOnlyOwnWorkspace(t *testing.T) {
	_, _, s := providerFixture(t)
	generation, err := biz.CheckpointWorkspaceName(s.ID, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if len(generation) != 63 {
		t.Fatalf("workspace name length %d", len(generation))
	}
	s.ProviderRefs.PVCName = generation
	s.ProviderRefs.PVCUID = "generation-uid"
	pod := renderPod(s, "sandbox-"+s.ID, labels(s))
	if !podMatches(pod, s) {
		t.Fatal("generation pod did not match")
	}
	volumes, _, err := unstructured.NestedSlice(pod.Object, "spec", "volumes")
	if err != nil {
		t.Fatal(err)
	}
	first := volumes[0].(map[string]any)
	mount := first["persistentVolumeClaim"].(map[string]any)
	if mount["claimName"] != generation {
		t.Fatalf("wrong claim: %#v", mount)
	}
	foreign, err := biz.CheckpointWorkspaceName("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	mount["claimName"] = foreign
	_ = unstructured.SetNestedSlice(pod.Object, volumes, "spec", "volumes")
	if podMatches(pod, s) {
		t.Fatal("another sandbox's workspace was accepted")
	}
	s.ProviderRefs.PVCName = foreign
	workspace, _ := names(s)
	if workspace != "" {
		t.Fatalf("foreign persisted name accepted: %s", workspace)
	}
}

func TestCheckpointPVCSourceValidationSeparatesOrdinaryAndRestored(t *testing.T) {
	_, p, s, x := transferProviderFixture(t, "Immediate")
	ordinary := renderWorkspace(s, "workspace-"+s.ID)
	if !pvcMatches(ordinary, s) {
		t.Fatal("ordinary PVC rejected")
	}
	_ = unstructured.SetNestedMap(ordinary.Object, map[string]any{"apiGroup": transferSnapshotAPIGroup, "kind": "VolumeSnapshot", "name": x.SourceSnapshotName}, "spec", "dataSource")
	if pvcMatches(ordinary, s) {
		t.Fatal("ordinary PVC accepted unsolicited source")
	}
	unstructured.RemoveNestedField(ordinary.Object, "spec", "dataSource")
	_ = unstructured.SetNestedMap(ordinary.Object, map[string]any{"apiGroup": transferSnapshotAPIGroup, "kind": "VolumeSnapshot", "name": x.SourceSnapshotName}, "spec", "dataSourceRef")
	if pvcMatches(ordinary, s) {
		t.Fatal("ordinary PVC accepted unsolicited source ref")
	}
	outcome, uid, err := p.CreateCheckpointTransferPVC(context.Background(), s, x, x.MutationID)
	if err != nil || outcome != biz.MutationApplied {
		t.Fatalf("transfer create: %s %v", outcome, err)
	}
	pvc, err := p.client.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(context.Background(), x.CandidateName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.transferPVCMatches(pvc, s, x, x.MutationID) {
		t.Fatal("trusted transfer source rejected")
	}
	s.ProviderRefs.PVCName = x.CandidateName
	s.ProviderRefs.PVCUID = uid
	s.ProviderRefs.PVCSourceSnapshotName = x.SourceSnapshotName
	s.ProviderRefs.PVCSourceSnapshotUID = x.SourceSnapshotUID
	s.ProviderRefs.PVCSourceContentUID = x.SourceContentUID
	s.ProviderRefs.PVCSourceSnapshotHandle = x.SourceSnapshotHandle
	s.ProviderRefs.PVCSourceMutationID = x.MutationID
	if !pvcMatches(pvc, s) {
		t.Fatal("committed restored PVC rejected")
	}
	s.ProviderRefs.PVCSourceSnapshotName = "other"
	if pvcMatches(pvc, s) {
		t.Fatal("restored PVC accepted wrong source")
	}
}
