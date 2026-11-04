package sandbox

import (
	"context"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var checkpointReadSnapshotGVR = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshots"}
var checkpointReadContentGVR = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshotcontents"}

// ObserveRecordedCheckpoint only follows a snapshot and content already bound
// to this tenant/Sandbox in the durable ledger. Labels and object names alone
// never establish ownership. CSI restoreSize is not actual snapshot size.
func (p *KubernetesProvider) ObserveRecordedCheckpoint(ctx context.Context, s biz.Sandbox, c biz.Checkpoint) (biz.CheckpointObservation, error) {
	if err := biz.RequireTenant(ctx, s.TenantID); err != nil {
		return biz.CheckpointObservation{}, err
	}
	return p.observeRecordedCheckpoint(ctx, s, c, false)
}

// observeRecordedCheckpoint is also used by the internal cleanup worker,
// whose tenant and checkpoint come from the locked durable journal.
func (p *KubernetesProvider) observeRecordedCheckpoint(ctx context.Context, s biz.Sandbox, c biz.Checkpoint, forCleanup bool) (biz.CheckpointObservation, error) {
	var out biz.CheckpointObservation
	if !biz.UUID(s.ID) || !biz.UUID(c.ID) || c.TenantID != s.TenantID || c.SandboxID != s.ID || c.Source.ClusterID != s.Snapshot.ClusterID || c.Source.Namespace != s.Snapshot.Namespace || c.Source.NamespaceUID != s.Snapshot.NamespaceUID {
		return out, biz.ErrOwnership
	}
	if err := p.requireCluster(s); err != nil {
		return out, err
	}
	if c.State != "ready" {
		return biz.CheckpointObservation{State: "not_observed"}, nil
	}
	if c.Phase != "done" || c.MutationID != "" || c.CompletedAt == nil || c.SnapshotName == "" || c.SnapshotUID == "" || c.ContentName == "" || c.ContentUID == "" || c.SnapshotHandle == "" || c.SnapshotMutationID == "" || c.Source.PVCName == "" || c.Source.PVCUID == "" || c.Source.PVUID == "" || c.Source.VolumeHandle == "" || c.Source.CSIDriver == "" || c.Source.SnapshotClass == "" {
		return biz.CheckpointObservation{State: "identity_unknown", Reason: "CHECKPOINT_IDENTITY_INCOMPLETE"}, nil
	}
	nsUID, err := p.verifyTenantNamespace(ctx, s, "")
	if err != nil {
		return out, err
	}
	finish := func(state, reason string) (biz.CheckpointObservation, error) {
		if _, err := p.verifyTenantNamespace(ctx, s, nsUID); err != nil {
			return out, err
		}
		return biz.CheckpointObservation{State: state, Reason: reason}, nil
	}
	snap, err := p.client.Resource(checkpointReadSnapshotGVR).Namespace(s.Snapshot.Namespace).Get(ctx, c.SnapshotName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return finish("missing", "CHECKPOINT_PROVIDER_MISSING")
	}
	if err != nil {
		return out, err
	}
	if string(snap.GetUID()) != c.SnapshotUID || snap.GetResourceVersion() == "" || !owned(snap, s) || snap.GetAnnotations()[providerMutationID] != c.SnapshotMutationID {
		return finish("identity_mismatch", "CHECKPOINT_IDENTITY_MISMATCH")
	}
	source, found, err := unstructured.NestedString(snap.Object, "spec", "source", "persistentVolumeClaimName")
	if err != nil || !found || source != c.Source.PVCName {
		return finish("identity_mismatch", "CHECKPOINT_IDENTITY_MISMATCH")
	}
	imported, importedFound, importedErr := unstructured.NestedString(snap.Object, "spec", "source", "volumeSnapshotContentName")
	if importedErr != nil || (importedFound && imported != "") {
		return finish("identity_mismatch", "CHECKPOINT_IDENTITY_MISMATCH")
	}
	class, found, err := unstructured.NestedString(snap.Object, "spec", "volumeSnapshotClassName")
	if err != nil || !found || class != c.Source.SnapshotClass {
		return finish("identity_mismatch", "CHECKPOINT_IDENTITY_MISMATCH")
	}
	bound, found, err := unstructured.NestedString(snap.Object, "status", "boundVolumeSnapshotContentName")
	if err != nil || !found || bound != c.ContentName {
		return finish("identity_mismatch", "CHECKPOINT_IDENTITY_MISMATCH")
	}
	content, err := p.client.Resource(checkpointReadContentGVR).Get(ctx, c.ContentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return finish("missing", "CHECKPOINT_PROVIDER_MISSING")
	}
	if err != nil {
		return out, err
	}
	refName, _, _ := unstructured.NestedString(content.Object, "spec", "volumeSnapshotRef", "name")
	refNS, _, _ := unstructured.NestedString(content.Object, "spec", "volumeSnapshotRef", "namespace")
	refUID, _, _ := unstructured.NestedString(content.Object, "spec", "volumeSnapshotRef", "uid")
	driver, _, _ := unstructured.NestedString(content.Object, "spec", "driver")
	handle, _, _ := unstructured.NestedString(content.Object, "spec", "source", "volumeHandle")
	policy, _, _ := unstructured.NestedString(content.Object, "spec", "deletionPolicy")
	contentClass, _, _ := unstructured.NestedString(content.Object, "spec", "volumeSnapshotClassName")
	if string(content.GetUID()) != c.ContentUID || content.GetResourceVersion() == "" || refName != c.SnapshotName || refNS != s.Snapshot.Namespace || refUID != c.SnapshotUID || driver != c.Source.CSIDriver || handle != c.Source.VolumeHandle || policy != "Delete" || contentClass != c.Source.SnapshotClass {
		return finish("identity_mismatch", "CHECKPOINT_IDENTITY_MISMATCH")
	}
	ready, _, err := unstructured.NestedBool(snap.Object, "status", "readyToUse")
	if err != nil {
		return out, biz.ErrDependencyUnavailable
	}
	contentReady, _, err := unstructured.NestedBool(content.Object, "status", "readyToUse")
	if err != nil {
		return out, biz.ErrDependencyUnavailable
	}
	snapshotHandle, _, err := unstructured.NestedString(content.Object, "status", "snapshotHandle")
	if err != nil {
		return out, biz.ErrDependencyUnavailable
	}
	if snapshotHandle != "" && snapshotHandle != c.SnapshotHandle {
		return finish("identity_mismatch", "CHECKPOINT_IDENTITY_MISMATCH")
	}
	snapshotError, snapshotErrorFound, err := unstructured.NestedFieldNoCopy(snap.Object, "status", "error")
	if err != nil {
		return out, biz.ErrDependencyUnavailable
	}
	contentError, contentErrorFound, err := unstructured.NestedFieldNoCopy(content.Object, "status", "error")
	if err != nil {
		return out, biz.ErrDependencyUnavailable
	}
	currentSnap, err := p.client.Resource(checkpointReadSnapshotGVR).Namespace(s.Snapshot.Namespace).Get(ctx, c.SnapshotName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	currentContent, err := p.client.Resource(checkpointReadContentGVR).Get(ctx, c.ContentName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	if !checkpointReadUnchanged(snap, currentSnap) || !checkpointReadUnchanged(content, currentContent) {
		return out, biz.ErrDependencyUnavailable
	}
	if snap.GetDeletionTimestamp() != nil || content.GetDeletionTimestamp() != nil {
		return finish("deleting", "CHECKPOINT_PROVIDER_DELETING")
	}
	// A previously completed snapshot can still be deleted while temporarily
	// not ready, but only if its exact backend handle is still verifiable.
	if forCleanup && snapshotHandle == c.SnapshotHandle && snapshotHandle != "" {
		return finish("deletable", "")
	}
	if !ready || !contentReady || snapshotHandle == "" || (snapshotErrorFound && snapshotError != nil) || (contentErrorFound && contentError != nil) {
		return finish("not_ready", "CHECKPOINT_PROVIDER_NOT_READY")
	}
	return finish("ready", "")
}
func checkpointReadUnchanged(a, b *unstructured.Unstructured) bool {
	return a != nil && b != nil && a.GetUID() != "" && a.GetUID() == b.GetUID() && a.GetResourceVersion() != "" && a.GetResourceVersion() == b.GetResourceVersion()
}
