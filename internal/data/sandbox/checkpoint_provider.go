package sandbox

// CSI snapshot observations and writes are kept private to the checkpoint
// worker. They must only be called after the database has recorded the exact
// operation and source identities. A preflight read is not a write fence.
import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/sandbox"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var checkpointSnapshotClassGVR = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshotclasses"}

type checkpointWorkspaceIdentity struct {
	NamespaceUID, PVCName, PVCUID, PVName, PVUID, Driver, VolumeHandle string
}

type checkpointClassIdentity struct{ Name, UID, ResourceVersion string }

func (p *KubernetesProvider) checkpointWorkspace(ctx context.Context, s biz.Sandbox) (checkpointWorkspaceIdentity, error) {
	var out checkpointWorkspaceIdentity
	if err := p.requireCluster(s); err != nil {
		return out, err
	}
	uid, err := p.verifyTenantNamespace(ctx, s, "")
	if err != nil {
		return out, err
	}
	pvcName, _ := names(s)
	if s.ProviderRefs.PVCName != pvcName || s.ProviderRefs.PVCUID == "" || s.ProviderRefs.PVName == "" || s.ProviderRefs.PVUID == "" {
		return out, biz.ErrDataPlaneUnavailable
	}
	pvc, err := p.client.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	phase, _, _ := unstructured.NestedString(pvc.Object, "status", "phase")
	pvName, _, _ := unstructured.NestedString(pvc.Object, "spec", "volumeName")
	if !owned(pvc, s) || !pvcMatches(pvc, s) || string(pvc.GetUID()) != s.ProviderRefs.PVCUID || pvc.GetResourceVersion() == "" || pvc.GetDeletionTimestamp() != nil || phase != "Bound" || pvName != s.ProviderRefs.PVName {
		return out, biz.ErrOwnership
	}
	pv, err := p.client.Resource(pvGVR).Get(ctx, pvName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	claimNS, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "namespace")
	claimName, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "name")
	claimUID, _, _ := unstructured.NestedString(pv.Object, "spec", "claimRef", "uid")
	pvPhase, _, _ := unstructured.NestedString(pv.Object, "status", "phase")
	pvClass, _, _ := unstructured.NestedString(pv.Object, "spec", "storageClassName")
	policy, _, _ := unstructured.NestedString(pv.Object, "spec", "persistentVolumeReclaimPolicy")
	driver, _, _ := unstructured.NestedString(pv.Object, "spec", "csi", "driver")
	handle, _, _ := unstructured.NestedString(pv.Object, "spec", "csi", "volumeHandle")
	if string(pv.GetUID()) != s.ProviderRefs.PVUID || pv.GetResourceVersion() == "" || pv.GetDeletionTimestamp() != nil || pvPhase != "Bound" || claimNS != s.Snapshot.Namespace || claimName != pvcName || claimUID != s.ProviderRefs.PVCUID || pvClass != s.Snapshot.Workspace.StorageClass || policy != "Delete" {
		return out, biz.ErrOwnership
	}
	if driver == "" || handle == "" {
		return out, biz.ErrUnsupported
	}
	pvcAgain, err := p.client.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	pvAgain, err := p.client.Resource(pvGVR).Get(ctx, pvName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	if !checkpointUnchanged(pvc, pvcAgain) || !checkpointUnchanged(pv, pvAgain) {
		return out, biz.ErrDependencyUnavailable
	}
	if _, err = p.verifyTenantNamespace(ctx, s, uid); err != nil {
		return out, err
	}
	return checkpointWorkspaceIdentity{NamespaceUID: uid, PVCName: pvcName, PVCUID: string(pvc.GetUID()), PVName: pvName, PVUID: string(pv.GetUID()), Driver: driver, VolumeHandle: handle}, nil
}

func checkpointUnchanged(a, b *unstructured.Unstructured) bool {
	return a != nil && b != nil && a.GetUID() != "" && a.GetUID() == b.GetUID() && a.GetResourceVersion() != "" && a.GetResourceVersion() == b.GetResourceVersion() && b.GetDeletionTimestamp() == nil
}

// No default is assumed. Exactly one Delete class must match the bound PV's
// CSI driver; a partial or inconsistent list cannot establish uniqueness.
func (p *KubernetesProvider) checkpointSelectClass(ctx context.Context, driver string) (checkpointClassIdentity, error) {
	var out checkpointClassIdentity
	if driver == "" {
		return out, biz.ErrInvalid
	}
	opts := metav1.ListOptions{Limit: 200}
	var revision string
	var selected *unstructured.Unstructured
	for page := 0; ; page++ {
		if page >= 10 {
			return out, fmt.Errorf("%w: snapshot class list exceeds limit", biz.ErrDependencyUnavailable)
		}
		list, err := p.client.Resource(checkpointSnapshotClassGVR).List(ctx, opts)
		if err != nil {
			return out, err
		}
		if list.GetResourceVersion() == "" || (revision != "" && revision != list.GetResourceVersion()) {
			return out, biz.ErrDependencyUnavailable
		}
		revision = list.GetResourceVersion()
		for i := range list.Items {
			obj := &list.Items[i]
			d, _, derr := unstructured.NestedString(obj.Object, "driver")
			policy, _, perr := unstructured.NestedString(obj.Object, "deletionPolicy")
			if derr != nil || perr != nil {
				return out, biz.ErrDependencyUnavailable
			}
			if d != driver || policy != "Delete" || obj.GetDeletionTimestamp() != nil {
				continue
			}
			if selected != nil || obj.GetUID() == "" || obj.GetResourceVersion() == "" || obj.GetName() == "" {
				return out, biz.ErrDependencyUnavailable
			}
			selected = obj.DeepCopy()
		}
		next := list.GetContinue()
		if next == "" {
			break
		}
		if next == opts.Continue {
			return out, biz.ErrDependencyUnavailable
		}
		opts.Continue = next
	}
	if selected == nil {
		return out, biz.ErrUnsupported
	}
	current, err := p.client.Resource(checkpointSnapshotClassGVR).Get(ctx, selected.GetName(), metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	if !checkpointUnchanged(selected, current) {
		return out, biz.ErrDependencyUnavailable
	}
	return checkpointClassIdentity{Name: selected.GetName(), UID: string(selected.GetUID()), ResourceVersion: selected.GetResourceVersion()}, nil
}

var checkpointSnapshotGVR = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshots"}
var checkpointContentGVR = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshotcontents"}

// Snapshot and content identities must be committed as receipts before they
// are used for a subsequent restore/clone or a managed delete.
type checkpointProviderObservation struct {
	SnapshotUID, ContentName, ContentUID, SnapshotHandle string
	State, Reason                                        string
	MinimumRestoreBytes                                  int64
	MinimumRestoreKnown                                  bool
}

func checkpointHasStatusError(obj *unstructured.Unstructured) (bool, error) {
	v, found, err := unstructured.NestedFieldNoCopy(obj.Object, "status", "error")
	if err != nil {
		return false, biz.ErrDependencyUnavailable
	}
	if !found || v == nil {
		return false, nil
	}
	if _, ok := v.(map[string]any); !ok {
		return false, biz.ErrDependencyUnavailable
	}
	return true, nil
}

func (p *KubernetesProvider) checkpointObserve(ctx context.Context, s biz.Sandbox, checkpointID, mutationID, snapshotUID, contentUID string, source checkpointWorkspaceIdentity, class checkpointClassIdentity) (checkpointProviderObservation, error) {
	var out checkpointProviderObservation
	if !biz.UUID(checkpointID) || !biz.UUID(mutationID) || source.PVCUID == "" || source.PVUID == "" || source.VolumeHandle == "" || source.Driver == "" || class.Name == "" || class.UID == "" {
		return out, biz.ErrInvalid
	}
	if err := p.requireCluster(s); err != nil {
		return out, err
	}
	if source.NamespaceUID != s.Snapshot.NamespaceUID || source.PVCName != s.ProviderRefs.PVCName || source.PVCUID != s.ProviderRefs.PVCUID || source.PVName != s.ProviderRefs.PVName || source.PVUID != s.ProviderRefs.PVUID {
		return out, biz.ErrOwnership
	}
	if _, err := p.verifyTenantNamespace(ctx, s, source.NamespaceUID); err != nil {
		return out, err
	}
	classObject, err := p.client.Resource(checkpointSnapshotClassGVR).Get(ctx, class.Name, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	classDriver, _, classDriverErr := unstructured.NestedString(classObject.Object, "driver")
	classPolicy, _, classPolicyErr := unstructured.NestedString(classObject.Object, "deletionPolicy")
	if classDriverErr != nil || classPolicyErr != nil || string(classObject.GetUID()) != class.UID || classObject.GetResourceVersion() != class.ResourceVersion || class.ResourceVersion == "" || classObject.GetDeletionTimestamp() != nil || classDriver != source.Driver || classPolicy != "Delete" {
		return out, biz.ErrOwnership
	}
	name := "sandbox-checkpoint-" + checkpointID
	snap, err := p.client.Resource(checkpointSnapshotGVR).Namespace(s.Snapshot.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, verifyErr := p.verifyTenantNamespace(ctx, s, source.NamespaceUID); verifyErr != nil {
			return out, verifyErr
		}
		out.State = "absent"
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if !owned(snap, s) || snap.GetUID() == "" || snap.GetResourceVersion() == "" || snap.GetAnnotations()[providerMutationID] != mutationID || (snapshotUID != "" && string(snap.GetUID()) != snapshotUID) {
		return out, biz.ErrOwnership
	}
	pvcName, found, err := unstructured.NestedString(snap.Object, "spec", "source", "persistentVolumeClaimName")
	if err != nil || !found || pvcName != source.PVCName {
		return out, biz.ErrOwnership
	}
	other, found, err := unstructured.NestedString(snap.Object, "spec", "source", "volumeSnapshotContentName")
	if err != nil || (found && other != "") {
		return out, biz.ErrOwnership
	}
	className, found, err := unstructured.NestedString(snap.Object, "spec", "volumeSnapshotClassName")
	if err != nil || !found || className != class.Name {
		return out, biz.ErrOwnership
	}
	out.SnapshotUID = string(snap.GetUID())
	out.State = "creating"
	if snap.GetDeletionTimestamp() != nil {
		out.State = "deleting"
		return out, nil
	}
	snapshotError, err := checkpointHasStatusError(snap)
	if err != nil {
		return out, err
	}
	if snapshotError {
		out.Reason = "CSI_SNAPSHOT_LAST_ERROR"
	}
	ready, _, err := unstructured.NestedBool(snap.Object, "status", "readyToUse")
	if err != nil {
		return out, biz.ErrDependencyUnavailable
	}
	contentName, found, err := unstructured.NestedString(snap.Object, "status", "boundVolumeSnapshotContentName")
	if err != nil {
		return out, biz.ErrDependencyUnavailable
	}
	if !found || contentName == "" {
		if ready {
			return out, biz.ErrOwnership
		}
		return out, nil
	}
	content, err := p.client.Resource(checkpointContentGVR).Get(ctx, contentName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	if content.GetUID() == "" || content.GetResourceVersion() == "" || (contentUID != "" && string(content.GetUID()) != contentUID) {
		return out, biz.ErrOwnership
	}
	refName, _, _ := unstructured.NestedString(content.Object, "spec", "volumeSnapshotRef", "name")
	refNS, _, _ := unstructured.NestedString(content.Object, "spec", "volumeSnapshotRef", "namespace")
	refUID, _, _ := unstructured.NestedString(content.Object, "spec", "volumeSnapshotRef", "uid")
	driver, _, _ := unstructured.NestedString(content.Object, "spec", "driver")
	handle, _, _ := unstructured.NestedString(content.Object, "spec", "source", "volumeHandle")
	policy, _, _ := unstructured.NestedString(content.Object, "spec", "deletionPolicy")
	contentClass, _, _ := unstructured.NestedString(content.Object, "spec", "volumeSnapshotClassName")
	if refName != name || refNS != s.Snapshot.Namespace || refUID != out.SnapshotUID || driver != source.Driver || handle != source.VolumeHandle || policy != "Delete" || contentClass != class.Name {
		return out, biz.ErrOwnership
	}
	out.ContentName, out.ContentUID = contentName, string(content.GetUID())
	if content.GetDeletionTimestamp() != nil {
		out.State = "deleting"
		return out, nil
	}
	contentError, err := checkpointHasStatusError(content)
	if err != nil {
		return out, err
	}
	if contentError {
		out.Reason = "CSI_SNAPSHOT_LAST_ERROR"
	}
	contentReady, _, err := unstructured.NestedBool(content.Object, "status", "readyToUse")
	if err != nil {
		return out, biz.ErrDependencyUnavailable
	}
	snapshotHandle, _, err := unstructured.NestedString(content.Object, "status", "snapshotHandle")
	if err != nil {
		return out, biz.ErrDependencyUnavailable
	}
	out.SnapshotHandle = snapshotHandle
	if ready && contentReady && snapshotHandle != "" && !snapshotError && !contentError {
		out.State = "ready"
		out.Reason = ""
	}
	size, sizeFound, err := unstructured.NestedString(snap.Object, "status", "restoreSize")
	if err != nil {
		return out, biz.ErrDependencyUnavailable
	}
	if sizeFound {
		q, err := resource.ParseQuantity(size)
		if err != nil || q.Sign() < 0 {
			return out, biz.ErrDependencyUnavailable
		}
		out.MinimumRestoreBytes, out.MinimumRestoreKnown = q.Value(), true
	}
	// Cross-resource reads are not atomic; repeat immutable-identity and RV
	// checks. This detects races but is not a substitute for the PVC finalizer.
	snapAgain, err := p.client.Resource(checkpointSnapshotGVR).Namespace(s.Snapshot.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	contentAgain, err := p.client.Resource(checkpointContentGVR).Get(ctx, contentName, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	if !checkpointUnchanged(snap, snapAgain) || !checkpointUnchanged(content, contentAgain) {
		return out, biz.ErrDependencyUnavailable
	}
	classAgain, err := p.client.Resource(checkpointSnapshotClassGVR).Get(ctx, class.Name, metav1.GetOptions{})
	if err != nil {
		return out, err
	}
	if !checkpointUnchanged(classObject, classAgain) {
		return out, biz.ErrDependencyUnavailable
	}
	if _, err = p.verifyTenantNamespace(ctx, s, source.NamespaceUID); err != nil {
		return out, err
	}
	return out, nil
}

const checkpointPVCFinalizer = "ani.io/sandbox-checkpoint-protection"
const checkpointPVCProtectionID = "ani.io/sandbox-checkpoint-protection-id"

type checkpointProviderReceipt struct{ Outcome, Reason, UID string }

// checkpointProtectPVC performs one UID/resourceVersion-fenced write. The
// caller must durably journal this exact protection mutation before dispatch;
// a transport failure is unknown, not permission to drop the barrier.
func (p *KubernetesProvider) checkpointProtectPVC(ctx context.Context, s biz.Sandbox, source checkpointWorkspaceIdentity, protectionID string, protect bool) (checkpointProviderReceipt, error) {
	r := checkpointProviderReceipt{Outcome: biz.MutationNotSent}
	if !biz.UUID(protectionID) {
		return r, biz.ErrInvalid
	}
	if err := p.requireCluster(s); err != nil {
		return r, err
	}
	if source.NamespaceUID != s.Snapshot.NamespaceUID || source.PVCName != s.ProviderRefs.PVCName || source.PVCUID == "" || source.PVCUID != s.ProviderRefs.PVCUID {
		return r, biz.ErrOwnership
	}
	if _, err := p.verifyTenantNamespace(ctx, s, source.NamespaceUID); err != nil {
		return r, err
	}
	api := p.client.Resource(pvcGVR).Namespace(s.Snapshot.Namespace)
	pvc, err := api.Get(ctx, source.PVCName, metav1.GetOptions{})
	if err != nil {
		return r, err
	}
	if !owned(pvc, s) || string(pvc.GetUID()) != source.PVCUID || pvc.GetResourceVersion() == "" || (protect && pvc.GetDeletionTimestamp() != nil) {
		return r, biz.ErrOwnership
	}
	annotations := pvc.GetAnnotations()
	marker := annotations[checkpointPVCProtectionID]
	finalizers := pvc.GetFinalizers()
	found := false
	for _, f := range finalizers {
		if f == checkpointPVCFinalizer {
			if found {
				return r, biz.ErrOwnership
			}
			found = true
		}
	}
	if protect && ((marker != "" && marker != protectionID) || (found && marker != protectionID)) {
		return r, biz.ErrOwnership
	}
	if !protect && (marker != protectionID || !found) {
		return r, biz.ErrOwnership
	}
	if protect && marker == protectionID && found {
		return checkpointProviderReceipt{Outcome: biz.MutationApplied, UID: source.PVCUID}, nil
	}
	copyAnnotations := map[string]string{}
	for k, v := range annotations {
		copyAnnotations[k] = v
	}
	newFinalizers := make([]string, 0, len(finalizers)+1)
	for _, f := range finalizers {
		if f != checkpointPVCFinalizer {
			newFinalizers = append(newFinalizers, f)
		}
	}
	if protect {
		copyAnnotations[checkpointPVCProtectionID] = protectionID
		newFinalizers = append(newFinalizers, checkpointPVCFinalizer)
	} else {
		delete(copyAnnotations, checkpointPVCProtectionID)
	}
	ops := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": source.PVCUID}, {"op": "test", "path": "/metadata/resourceVersion", "value": pvc.GetResourceVersion()}, {"op": "add", "path": "/metadata/annotations", "value": copyAnnotations}, {"op": "add", "path": "/metadata/finalizers", "value": newFinalizers}}
	payload, err := json.Marshal(ops)
	if err != nil {
		return r, err
	}
	var trace checkpointWriteTrace
	result, err := api.Patch(trace.context(ctx), source.PVCName, types.JSONPatchType, payload, metav1.PatchOptions{})
	r.Outcome = trace.outcome(err)
	if err != nil {
		return r, err
	}
	if result == nil || string(result.GetUID()) != source.PVCUID || !maps.Equal(result.GetAnnotations(), copyAnnotations) || !slices.Equal(slices.Sorted(slices.Values(result.GetFinalizers())), slices.Sorted(slices.Values(newFinalizers))) {
		r.Outcome = biz.MutationUnknown
		return r, biz.ErrOwnership
	}
	if _, err = p.verifyTenantNamespace(ctx, s, source.NamespaceUID); err != nil {
		r.Outcome = biz.MutationUnknown
		return r, err
	}
	r.UID = string(result.GetUID())
	return r, nil
}

// checkpointCreateSnapshot sends one CSI create after verifying the persisted
// source, selected class and PVC protection. The caller journals mutationID
// before calling. After an uncertain response, absence is never conclusive.
func (p *KubernetesProvider) checkpointCreateSnapshot(ctx context.Context, s biz.Sandbox, checkpointID, mutationID, protectionID string, source checkpointWorkspaceIdentity, class checkpointClassIdentity) (checkpointProviderReceipt, error) {
	r := checkpointProviderReceipt{Outcome: biz.MutationNotSent}
	if !biz.UUID(checkpointID) || !biz.UUID(mutationID) || !biz.UUID(protectionID) || class.Name == "" || class.UID == "" {
		return r, biz.ErrInvalid
	}
	current, err := p.checkpointWorkspace(ctx, s)
	if err != nil {
		return r, err
	}
	if current != source {
		return r, biz.ErrOwnership
	}
	pvc, err := p.client.Resource(pvcGVR).Namespace(s.Snapshot.Namespace).Get(ctx, source.PVCName, metav1.GetOptions{})
	if err != nil {
		return r, err
	}
	protected := false
	for _, f := range pvc.GetFinalizers() {
		if f == checkpointPVCFinalizer {
			protected = true
		}
	}
	if string(pvc.GetUID()) != source.PVCUID || pvc.GetDeletionTimestamp() != nil || !protected || pvc.GetAnnotations()[checkpointPVCProtectionID] != protectionID {
		return r, biz.ErrOwnership
	}
	c, err := p.client.Resource(checkpointSnapshotClassGVR).Get(ctx, class.Name, metav1.GetOptions{})
	if err != nil {
		return r, err
	}
	driver, _, _ := unstructured.NestedString(c.Object, "driver")
	policy, _, _ := unstructured.NestedString(c.Object, "deletionPolicy")
	if string(c.GetUID()) != class.UID || class.ResourceVersion == "" || c.GetResourceVersion() != class.ResourceVersion || c.GetDeletionTimestamp() != nil || driver != source.Driver || policy != "Delete" {
		return r, biz.ErrOwnership
	}
	name := "sandbox-checkpoint-" + checkpointID
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "snapshot.storage.k8s.io/v1", "kind": "VolumeSnapshot", "metadata": map[string]any{"name": name, "namespace": s.Snapshot.Namespace, "labels": stringMap(labels(s)), "annotations": map[string]any{providerMutationID: mutationID}}, "spec": map[string]any{"volumeSnapshotClassName": class.Name, "source": map[string]any{"persistentVolumeClaimName": source.PVCName}}}}
	var trace checkpointWriteTrace
	created, err := p.client.Resource(checkpointSnapshotGVR).Namespace(s.Snapshot.Namespace).Create(trace.context(ctx), obj, metav1.CreateOptions{})
	r.Outcome = trace.outcome(err)
	if err != nil {
		return r, err
	}
	if created == nil || created.GetUID() == "" || !owned(created, s) || created.GetAnnotations()[providerMutationID] != mutationID {
		r.Outcome = biz.MutationUnknown
		return r, biz.ErrOwnership
	}
	if _, err = p.verifyTenantNamespace(ctx, s, source.NamespaceUID); err != nil {
		r.Outcome = biz.MutationUnknown
		return r, err
	}
	r.UID = string(created.GetUID())
	return r, nil
}
