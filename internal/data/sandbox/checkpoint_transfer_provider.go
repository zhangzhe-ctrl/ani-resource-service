package sandbox

import (
	"context"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ biz.CheckpointTransferProvider = (*KubernetesProvider)(nil)

const transferSnapshotAPIGroup = "snapshot.storage.k8s.io"

type transferSnapshotSource struct{ driver, sourceVolumeHandle string }

func (p *KubernetesProvider) transferSnapshot(ctx context.Context, s biz.Sandbox, t biz.CheckpointTransfer) (transferSnapshotSource, error) {
	var out transferSnapshotSource
	if err := p.requireCluster(s); err != nil {
		return out, err
	}
	candidateName, nameErr := biz.CheckpointWorkspaceName(s.ID, t.ID)
	if t.TenantID != s.TenantID || t.TargetSandboxID != s.ID || !biz.UUID(t.SourceSandboxID) || !biz.UUID(t.CheckpointID) || nameErr != nil || t.CandidateName != candidateName || t.SourceSnapshotName == "" || t.SourceSnapshotUID == "" || t.SourceContentUID == "" || t.SourceSnapshotHandle == "" {
		return out, biz.ErrOwnership
	}
	if _, err := p.verifyTenantNamespace(ctx, s, s.Snapshot.NamespaceUID); err != nil {
		return out, err
	}
	snap, err := p.client.Resource(checkpointSnapshotGVR).Namespace(s.Snapshot.Namespace).Get(ctx, t.SourceSnapshotName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	labels := snap.GetLabels()
	if string(snap.GetUID()) != t.SourceSnapshotUID || snap.GetNamespace() != s.Snapshot.Namespace || snap.GetResourceVersion() == "" || snap.GetDeletionTimestamp() != nil || labels[providerTenantLabel] != t.TenantID || labels[providerOwnerLabel] != t.SourceSandboxID || labels[providerServiceLabel] != "ani-resource-service" {
		return out, biz.ErrOwnership
	}
	ready, found, e := unstructured.NestedBool(snap.Object, "status", "readyToUse")
	if e != nil || !found || !ready {
		return out, biz.ErrDependencyUnavailable
	}
	hasError, e := checkpointHasStatusError(snap)
	if e != nil || hasError {
		return out, biz.ErrDependencyUnavailable
	}
	size, sizeFound, e := unstructured.NestedString(snap.Object, "status", "restoreSize")
	if e != nil {
		return out, biz.ErrDependencyUnavailable
	}
	if sizeFound {
		minimum, e := resource.ParseQuantity(size)
		if e != nil || minimum.Sign() < 0 {
			return out, biz.ErrDependencyUnavailable
		}
		target, e := resource.ParseQuantity(s.Snapshot.Workspace.Size)
		if e != nil || target.Cmp(minimum) < 0 {
			return out, biz.ErrUnsupported
		}
	}
	contentName, found, e := unstructured.NestedString(snap.Object, "status", "boundVolumeSnapshotContentName")
	if e != nil || !found || contentName == "" {
		return out, biz.ErrDependencyUnavailable
	}
	content, err := p.client.Resource(checkpointContentGVR).Get(ctx, contentName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	if string(content.GetUID()) != t.SourceContentUID || content.GetResourceVersion() == "" || content.GetDeletionTimestamp() != nil {
		return out, biz.ErrOwnership
	}
	refName, _, _ := unstructured.NestedString(content.Object, "spec", "volumeSnapshotRef", "name")
	refNS, _, _ := unstructured.NestedString(content.Object, "spec", "volumeSnapshotRef", "namespace")
	refUID, _, _ := unstructured.NestedString(content.Object, "spec", "volumeSnapshotRef", "uid")
	driver, _, _ := unstructured.NestedString(content.Object, "spec", "driver")
	volumeHandle, _, _ := unstructured.NestedString(content.Object, "spec", "source", "volumeHandle")
	policy, _, _ := unstructured.NestedString(content.Object, "spec", "deletionPolicy")
	snapHandle, _, _ := unstructured.NestedString(content.Object, "status", "snapshotHandle")
	contentReady, contentReadyFound, e := unstructured.NestedBool(content.Object, "status", "readyToUse")
	if e != nil {
		return out, biz.ErrDependencyUnavailable
	}
	contentError, e := checkpointHasStatusError(content)
	if e != nil || contentError {
		return out, biz.ErrDependencyUnavailable
	}
	if refName != t.SourceSnapshotName || refNS != s.Snapshot.Namespace || refUID != t.SourceSnapshotUID || driver == "" || volumeHandle == "" || policy != "Delete" || snapHandle != t.SourceSnapshotHandle || !contentReadyFound || !contentReady {
		return out, biz.ErrOwnership
	}
	again, err := p.client.Resource(checkpointSnapshotGVR).Namespace(s.Snapshot.Namespace).Get(ctx, t.SourceSnapshotName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	contentAgain, err := p.client.Resource(checkpointContentGVR).Get(ctx, contentName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	if !checkpointUnchanged(snap, again) || !checkpointUnchanged(content, contentAgain) {
		return out, biz.ErrDependencyUnavailable
	}
	if _, err = p.verifyTenantNamespace(ctx, s, s.Snapshot.NamespaceUID); err != nil {
		return out, err
	}
	return transferSnapshotSource{driver: driver, sourceVolumeHandle: volumeHandle}, nil
}

// ValidateCheckpointTransferStorage is a read-only pre-acceptance check.
// It is repeated immediately before provider creation to catch policy drift.
func (p *KubernetesProvider) ValidateCheckpointTransferStorage(ctx context.Context, name string) error {
	if name == "" {
		return biz.ErrInvalid
	}
	class, err := p.client.Resource(storageClassGVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	mode, found, err := unstructured.NestedString(class.Object, "volumeBindingMode")
	if err != nil || class.GetUID() == "" || class.GetResourceVersion() == "" || class.GetDeletionTimestamp() != nil {
		return biz.ErrDependencyUnavailable
	}
	if !found || mode == "" {
		mode = "Immediate"
	}
	if mode == "WaitForFirstConsumer" {
		return biz.ErrCheckpointWFFCUnsupported
	}
	if mode != "Immediate" {
		return biz.ErrUnsupported
	}
	return nil
}

func (p *KubernetesProvider) transferStorageClass(ctx context.Context, s biz.Sandbox, driver string) (string, error) {
	if s.Snapshot.Workspace.StorageClass == "" || s.Snapshot.Workspace.ReclaimPolicy != "Delete" {
		return "", biz.ErrUnsupported
	}
	class, err := p.client.Resource(storageClassGVR).Get(ctx, s.Snapshot.Workspace.StorageClass, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	provisioner, _, _ := unstructured.NestedString(class.Object, "provisioner")
	policy, _, _ := unstructured.NestedString(class.Object, "reclaimPolicy")
	mode, found, e := unstructured.NestedString(class.Object, "volumeBindingMode")
	if e != nil {
		return "", biz.ErrDependencyUnavailable
	}
	if !found || mode == "" {
		mode = "Immediate"
	}
	if class.GetUID() == "" || class.GetResourceVersion() == "" || class.GetDeletionTimestamp() != nil || provisioner != driver || policy != "Delete" || (mode != "Immediate" && mode != "WaitForFirstConsumer") {
		return "", biz.ErrUnsupported
	}
	return mode, nil
}
func transferDataSourceMatches(pvc *unstructured.Unstructured, t biz.CheckpointTransfer) bool {
	for _, field := range []string{"dataSource", "dataSourceRef"} {
		obj, found, err := unstructured.NestedMap(pvc.Object, "spec", field)
		if err != nil {
			return false
		}
		if !found {
			if field == "dataSource" {
				return false
			}
			continue
		}
		if obj["apiGroup"] != transferSnapshotAPIGroup || obj["kind"] != "VolumeSnapshot" || obj["name"] != t.SourceSnapshotName {
			return false
		}
		if ns, ok := obj["namespace"]; ok && ns != "" {
			return false
		}
	}
	return true
}
func (p *KubernetesProvider) transferPVCMatches(pvc *unstructured.Unstructured, s biz.Sandbox, t biz.CheckpointTransfer, mutationID string) bool {
	return owned(pvc, s) && pvc.GetName() == t.CandidateName && pvc.GetUID() != "" && pvc.GetResourceVersion() != "" && pvc.GetDeletionTimestamp() == nil && pvc.GetAnnotations()[providerMutationID] == mutationID && pvcBaseMatches(pvc, s) && transferDataSourceMatches(pvc, t) && (t.CandidateUID == "" || t.CandidateUID == string(pvc.GetUID()))
}
func (p *KubernetesProvider) CreateCheckpointTransferPVC(ctx context.Context, s biz.Sandbox, t biz.CheckpointTransfer, mutationID string) (string, string, error) {
	if !biz.UUID(mutationID) {
		return biz.MutationNotSent, "", biz.ErrInvalid
	}
	source, err := p.transferSnapshot(ctx, s, t)
	if err != nil {
		return biz.MutationNotSent, "", err
	}
	mode, err := p.transferStorageClass(ctx, s, source.driver)
	if err != nil {
		return biz.MutationNotSent, "", err
	}
	if mode == "WaitForFirstConsumer" {
		return biz.MutationNotSent, "", biz.ErrCheckpointWFFCUnsupported
	}
	obj := renderWorkspace(s, t.CandidateName)
	obj.SetAnnotations(map[string]string{providerMutationID: mutationID})
	if err = unstructured.SetNestedMap(obj.Object, map[string]any{"apiGroup": transferSnapshotAPIGroup, "kind": "VolumeSnapshot", "name": t.SourceSnapshotName}, "spec", "dataSource"); err != nil {
		return biz.MutationNotSent, "", err
	}
	created, err := p.client.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Create(ctx, obj, metav1.CreateOptions{})
	outcome := mutationOutcome(err)
	if err != nil {
		return outcome, "", err
	}
	if !p.transferPVCMatches(created, s, t, mutationID) {
		return biz.MutationUnknown, "", biz.ErrOwnership
	}
	if _, err = p.transferSnapshot(ctx, s, t); err != nil {
		return biz.MutationUnknown, "", err
	}
	return biz.MutationApplied, string(created.GetUID()), nil
}
func (p *KubernetesProvider) ObserveCheckpointTransferPVC(ctx context.Context, s biz.Sandbox, t biz.CheckpointTransfer) (biz.CheckpointTransferObservation, error) {
	var out biz.CheckpointTransferObservation
	if !biz.UUID(t.MutationID) {
		return out, biz.ErrInvalid
	}
	source, err := p.transferSnapshot(ctx, s, t)
	if err != nil {
		return out, err
	}
	mode, err := p.transferStorageClass(ctx, s, source.driver)
	if err != nil {
		return out, err
	}
	pvc, err := p.client.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(ctx, t.CandidateName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if !p.transferPVCMatches(pvc, s, t, t.MutationID) {
		return out, biz.ErrOwnership
	}
	out.Exists = true
	out.PVCName = pvc.GetName()
	out.PVCUID = string(pvc.GetUID())
	out.SnapshotUID = t.SourceSnapshotUID
	out.ContentUID = t.SourceContentUID
	out.SnapshotHandle = t.SourceSnapshotHandle
	phase, found, err := unstructured.NestedString(pvc.Object, "status", "phase")
	if err != nil {
		return biz.CheckpointTransferObservation{}, biz.ErrDependencyUnavailable
	}
	if !found || phase == "" || phase == "Pending" {
		out.Reason = "SANDBOX_TRANSFER_PVC_PENDING"
		if mode == "WaitForFirstConsumer" {
			out.Reason = "SANDBOX_TRANSFER_WAIT_FOR_FIRST_CONSUMER"
		}
		return out, nil
	}
	if phase != "Bound" {
		return biz.CheckpointTransferObservation{}, biz.ErrDependencyUnavailable
	}
	pvName, found, err := unstructured.NestedString(pvc.Object, "spec", "volumeName")
	if err != nil || !found || pvName == "" {
		return biz.CheckpointTransferObservation{}, biz.ErrDependencyUnavailable
	}
	pv, err := p.client.Resource(pvGVR).Get(ctx, pvName, metav1.GetOptions{})
	if err != nil {
		return biz.CheckpointTransferObservation{}, err
	}
	ns, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "namespace")
	name, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "name")
	uid, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "uid")
	pvClass, _, _ := unstructured.NestedString(pv.Object, "spec", "storageClassName")
	policy, _, _ := unstructured.NestedString(pv.Object, "spec", "persistentVolumeReclaimPolicy")
	pvPhase, _, _ := unstructured.NestedString(pv.Object, "status", "phase")
	driver, _, _ := unstructured.NestedString(pv.Object, "spec", "csi", "driver")
	handle, _, _ := unstructured.NestedString(pv.Object, "spec", "csi", "volumeHandle")
	if pv.GetUID() == "" || pv.GetResourceVersion() == "" || pv.GetDeletionTimestamp() != nil || pvPhase != "Bound" || ns != s.Snapshot.Namespace || name != t.CandidateName || uid != out.PVCUID || pvClass != s.Snapshot.Workspace.StorageClass || policy != "Delete" || driver != source.driver || handle == "" || handle == source.sourceVolumeHandle {
		return biz.CheckpointTransferObservation{}, biz.ErrOwnership
	}
	pvcAgain, err := p.client.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(ctx, t.CandidateName, metav1.GetOptions{})
	if err != nil {
		return biz.CheckpointTransferObservation{}, err
	}
	pvAgain, err := p.client.Resource(pvGVR).Get(ctx, pvName, metav1.GetOptions{})
	if err != nil {
		return biz.CheckpointTransferObservation{}, err
	}
	if !checkpointUnchanged(pvc, pvcAgain) || !checkpointUnchanged(pv, pvAgain) {
		return biz.CheckpointTransferObservation{}, biz.ErrDependencyUnavailable
	}
	if _, err = p.transferSnapshot(ctx, s, t); err != nil {
		return biz.CheckpointTransferObservation{}, err
	}
	out.Bound = true
	out.PVName = pvName
	out.PVUID = string(pv.GetUID())
	return out, nil
}
