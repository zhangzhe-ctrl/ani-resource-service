package sandbox

import (
	"context"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ biz.WorkspaceGenerationCleanupProvider = (*KubernetesProvider)(nil)

const csiPVReclaimFinalizer = "external-provisioner.volume.kubernetes.io/finalizer"

func (p *KubernetesProvider) ObserveWorkspaceGeneration(ctx context.Context, s biz.Sandbox, g biz.WorkspaceGenerationCleanup) (biz.WorkspaceGenerationObservation, error) {
	var out biz.WorkspaceGenerationObservation
	if err := p.requireCluster(s); err != nil {
		return out, err
	}
	if g.TenantID != s.TenantID || g.SandboxID != s.ID || !biz.UUID(g.GenerationID) || g.Refs.PVCUID == "" || g.Refs.PVUID == "" || g.Refs.PVName == "" || !biz.ValidWorkspaceName(s.ID, g.Refs.PVCName) {
		return out, biz.ErrOwnership
	}
	if _, err := p.verifyTenantNamespace(ctx, s, s.Snapshot.NamespaceUID); err != nil {
		return out, err
	}
	_, podName := names(s)
	pod, err := p.client.Resource(podGVR).Namespace(s.Snapshot.Namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return out, err
	}
	if pod != nil {
		if !owned(pod, s) {
			return out, biz.ErrOwnership
		}
		out.PodExists = true
		return out, nil
	}
	old := s
	old.ProviderRefs = g.Refs
	pvc, err := p.client.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(ctx, g.Refs.PVCName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return out, err
	}
	if pvc != nil && string(pvc.GetUID()) != g.Refs.PVCUID {
		return out, biz.ErrOwnership
	}
	if pvc != nil {
		if !owned(pvc, old) || !pvcMatches(pvc, old) || pvc.GetResourceVersion() == "" {
			return out, biz.ErrOwnership
		}
		out.PVCExists = true
	}
	pv, err := p.client.Resource(pvGVR).Get(ctx, g.Refs.PVName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return out, err
	}
	if pv != nil && string(pv.GetUID()) != g.Refs.PVUID {
		return out, biz.ErrOwnership
	}
	if pv != nil {
		ns, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "namespace")
		name, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "name")
		uid, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "uid")
		driver, _, _ := unstructured.NestedString(pv.Object, "spec", "csi", "driver")
		handle, _, _ := unstructured.NestedString(pv.Object, "spec", "csi", "volumeHandle")
		policy, _, _ := unstructured.NestedString(pv.Object, "spec", "persistentVolumeReclaimPolicy")
		class, _, _ := unstructured.NestedString(pv.Object, "spec", "storageClassName")
		finalizer := false
		for _, f := range pv.GetFinalizers() {
			if f == csiPVReclaimFinalizer {
				finalizer = true
			}
		}
		if pv.GetResourceVersion() == "" || ns != s.Snapshot.Namespace || name != g.Refs.PVCName || uid != g.Refs.PVCUID || driver == "" || handle == "" || policy != "Delete" || class != s.Snapshot.Workspace.StorageClass || !finalizer {
			return out, biz.ErrOwnership
		}
		if (g.Driver != "" && g.Driver != driver) || (g.VolumeHandle != "" && g.VolumeHandle != handle) {
			return out, biz.ErrOwnership
		}
		out.PVExists = true
		out.Driver = driver
		out.VolumeHandle = handle
	}
	if _, err = p.verifyTenantNamespace(ctx, s, s.Snapshot.NamespaceUID); err != nil {
		return biz.WorkspaceGenerationObservation{}, err
	}
	return out, nil
}
func (p *KubernetesProvider) DeleteWorkspaceGenerationPVC(ctx context.Context, s biz.Sandbox, g biz.WorkspaceGenerationCleanup, mutationID string) (string, error) {
	if !biz.UUID(mutationID) || g.Driver == "" || g.VolumeHandle == "" {
		return biz.MutationNotSent, biz.ErrInvalid
	}
	obs, err := p.ObserveWorkspaceGeneration(ctx, s, g)
	if err != nil {
		return biz.MutationNotSent, err
	}
	if obs.PodExists || !obs.PVCExists || !obs.PVExists || obs.Driver != g.Driver || obs.VolumeHandle != g.VolumeHandle {
		return biz.MutationNotSent, biz.ErrConflict
	}
	if _, err = p.verifyTenantNamespace(ctx, s, s.Snapshot.NamespaceUID); err != nil {
		return biz.MutationNotSent, err
	}
	err = p.client.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Delete(ctx, g.Refs.PVCName, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: ptrUID(g.Refs.PVCUID)}})
	if apierrors.IsNotFound(err) {
		err = nil
	}
	outcome := mutationOutcome(err)
	if err != nil {
		return outcome, err
	}
	if _, err = p.verifyTenantNamespace(ctx, s, s.Snapshot.NamespaceUID); err != nil {
		return biz.MutationUnknown, err
	}
	return biz.MutationApplied, nil
}
