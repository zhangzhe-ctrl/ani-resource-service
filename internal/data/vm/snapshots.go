package vm

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/vm"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var snapshotResource = schema.GroupVersionResource{Group: "snapshot.kubevirt.io", Version: "v1beta1", Resource: "virtualmachinesnapshots"}
var snapshotContentResource = schema.GroupVersionResource{Group: "snapshot.kubevirt.io", Version: "v1beta1", Resource: "virtualmachinesnapshotcontents"}
var restoreResource = schema.GroupVersionResource{Group: "snapshot.kubevirt.io", Version: "v1beta1", Resource: "virtualmachinerestores"}
var volumeSnapshotResource = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshots"}
var volumeSnapshotContentResource = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshotcontents"}
var volumeSnapshotClassResource = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshotclasses"}
var snapshotStorageResource = schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}
var snapshotProfileResource = schema.GroupVersionResource{Group: "cdi.kubevirt.io", Version: "v1beta1", Resource: "storageprofiles"}

// Called from the existing lease/version-fenced transaction, alongside disks.
func saveSnapshots(ctx context.Context, tx pgx.Tx, v biz.VM) error {
	for _, s := range v.Snapshots {
		_, err := tx.Exec(ctx, `INSERT INTO vm.snapshots(tenant_id,vm_id,snapshot_id,record) VALUES($1,$2,$3,$4) ON CONFLICT(tenant_id,vm_id,snapshot_id) DO UPDATE SET record=excluded.record`, v.TenantID, v.ID, s.ID, encode(s))
		if err != nil {
			return err
		}
	}
	return nil
}

func snapshotString(o *unstructured.Unstructured, path ...string) string {
	s, _, _ := unstructured.NestedString(o.Object, path...)
	return s
}
func snapshotBool(o *unstructured.Unstructured, path ...string) bool {
	b, _, _ := unstructured.NestedBool(o.Object, path...)
	return b
}
func snapshotOwner(o *unstructured.Unstructured, kind, name, uid string) bool {
	for _, owner := range o.GetOwnerReferences() {
		if owner.APIVersion == "snapshot.kubevirt.io/v1beta1" && owner.Kind == kind && owner.Name == name && string(owner.UID) == uid {
			return true
		}
	}
	return false
}
func snapshotRefs(refs []biz.ObjectRef, ref biz.ObjectRef) []biz.ObjectRef {
	if ref.UID == "" {
		return refs
	}
	for _, old := range refs {
		if old.Kind == ref.Kind && old.Namespace == ref.Namespace && old.Name == ref.Name {
			if old.UID != ref.UID {
				return refs
			}
			return refs
		}
	}
	return append(refs, ref)
}

// Follow KubeVirt's native class selection. No class is injected into a VM
// Snapshot request, and a Retain/ambiguous selection is never silently changed.
func (n *Native) snapshotClass(ctx context.Context, storageName string) (biz.ObjectRef, biz.ObjectRef, error) {
	sc, err := n.client.Resource(snapshotStorageResource).Get(ctx, storageName, metav1.GetOptions{})
	if err != nil {
		return biz.ObjectRef{}, biz.ObjectRef{}, err
	}
	driver := snapshotString(sc, "provisioner")
	if driver == "" {
		return biz.ObjectRef{}, biz.ObjectRef{}, biz.ErrDependency
	}
	name := ""
	profile, err := n.client.Resource(snapshotProfileResource).Get(ctx, storageName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return biz.ObjectRef{}, biz.ObjectRef{}, err
	}
	if err == nil {
		name = snapshotString(profile, "status", "snapshotClass")
	}
	if name == "" {
		list, err := n.client.Resource(volumeSnapshotClassResource).List(ctx, metav1.ListOptions{})
		if err != nil {
			return biz.ObjectRef{}, biz.ObjectRef{}, err
		}
		var candidates, defaults []string
		for i := range list.Items {
			item := &list.Items[i]
			if snapshotString(item, "driver") != driver {
				continue
			}
			candidates = append(candidates, item.GetName())
			if _, present := item.GetAnnotations()["snapshot.storage.kubernetes.io/is-default-class"]; present {
				defaults = append(defaults, item.GetName())
			}
		}
		if len(candidates) == 1 {
			name = candidates[0]
		} else if len(defaults) == 1 {
			name = defaults[0]
		} else {
			return biz.ObjectRef{}, biz.ObjectRef{}, biz.ErrDependency
		}
	}
	class, err := n.client.Resource(volumeSnapshotClassResource).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return biz.ObjectRef{}, biz.ObjectRef{}, err
	}
	if snapshotString(class, "driver") != driver || snapshotString(class, "deletionPolicy") != "Delete" || class.GetUID() == "" {
		return biz.ObjectRef{}, biz.ObjectRef{}, biz.ErrDependency
	}
	return objectRef(sc), objectRef(class), nil
}

func (n *Native) snapshotRoot(ctx context.Context, v biz.VM) (*unstructured.Unstructured, error) {
	if err := n.namespace(ctx, v); err != nil {
		return nil, err
	}
	root, err := n.client.Resource(vmResource).Namespace(v.Spec.Namespace).Get(ctx, v.Native.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if root.GetAPIVersion() != "kubevirt.io/v1" || root.GetKind() != "VirtualMachine" || v.Native.UID == "" || string(root.GetUID()) != v.Native.UID || !matches(root, v) || root.GetDeletionTimestamp() != nil {
		return nil, biz.ErrOwnership
	}
	return root, nil
}
func (n *Native) PrepareSnapshot(ctx context.Context, v biz.VM, s biz.Snapshot) (biz.Snapshot, error) {
	root, err := n.snapshotRoot(ctx, v)
	if err != nil {
		return s, err
	}
	volumes, _, _ := unstructured.NestedSlice(root.Object, "spec", "template", "spec", "volumes")
	if v.Spec.Media.PersistentTPM || v.Spec.Media.PersistentUEFI {
		list, err := n.client.Resource(pvcResource).Namespace(v.Spec.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "persistent-state-for=" + v.Native.Name})
		if err != nil {
			return s, err
		}
		var backend *unstructured.Unstructured
		for i := range list.Items {
			candidate := &list.Items[i]
			if candidate.GetDeletionTimestamp() != nil {
				continue
			}
			if backend != nil {
				return s, biz.ErrDependency
			}
			backend = candidate
		}
		if backend == nil || !owned(backend, "VirtualMachine", v.Native.UID) {
			return s, biz.ErrDependency
		}
		name := "persistent-state-for-" + v.Native.Name
		if len(name) > 63 {
			return s, biz.ErrDependency
		}
		s.Required = append(s.Required, biz.SnapshotVolume{Name: name, Role: "backend", PVC: objectRef(backend)})
	}
	for i, volume := range s.Required {
		pvc, err := n.client.Resource(pvcResource).Namespace(v.Spec.Namespace).Get(ctx, volume.PVC.Name, metav1.GetOptions{})
		if err != nil {
			return s, err
		}
		if volume.PVC.UID == "" || volume.PVC.Namespace != v.Spec.Namespace || string(pvc.GetUID()) != volume.PVC.UID {
			return s, biz.ErrOwnership
		}
		ownerValid := owned(pvc, "VirtualMachine", v.Native.UID)
		for _, disk := range s.Inventory {
			if disk.ID == volume.DiskID && owned(pvc, "DataVolume", disk.DV.UID) {
				ownerValid = true
			}
		}
		for _, copy := range s.MediaCopies {
			if copy.ID == volume.MediaCopyID && owned(pvc, "DataVolume", copy.DV.UID) {
				ownerValid = true
			}
		}
		if !ownerValid {
			return s, biz.ErrOwnership
		}
		if volume.Role != "backend" {
			_, native, err := namedItems(volumes, volume.Name)
			if err != nil {
				return s, err
			}
			if native == nil {
				return s, biz.ErrChanged
			}
			claim, _, _ := unstructured.NestedString(native, "persistentVolumeClaim", "claimName")
			dv, _, _ := unstructured.NestedString(native, "dataVolume", "name")
			if claim != volume.PVC.Name && dv != volume.PVC.Name {
				return s, biz.ErrChanged
			}
		}
		mode := snapshotString(pvc, "spec", "volumeMode")
		if mode == "" {
			mode = "Filesystem"
		}
		if (volume.Role == "system" || volume.Role == "data") && mode != "Block" {
			return s, biz.ErrOwnership
		}
		if volume.Role == "backend" && mode != "Filesystem" {
			return s, biz.ErrOwnership
		}
		storageName := snapshotString(pvc, "spec", "storageClassName")
		if storageName == "" {
			return s, biz.ErrDependency
		}
		storage, class, err := n.snapshotClass(ctx, storageName)
		if err != nil {
			return s, err
		}
		if storageName == v.Spec.StorageClass && storage.UID != v.Spec.StorageClassUID {
			return s, biz.ErrChanged
		}
		volume.Storage = storage
		volume.Class = class
		s.Required[i] = volume
	}
	return s, nil
}
func (n *Native) snapshotClassesUnchanged(ctx context.Context, s biz.Snapshot) error {
	for _, volume := range s.Required {
		storage, class, err := n.snapshotClass(ctx, volume.Storage.Name)
		if err != nil {
			return err
		}
		if storage != volume.Storage || class != volume.Class {
			return biz.ErrChanged
		}
	}
	return nil
}
func validSnapshotObject(o *unstructured.Unstructured, v biz.VM, s biz.Snapshot) bool {
	return o.GetAPIVersion() == "snapshot.kubevirt.io/v1beta1" && o.GetKind() == "VirtualMachineSnapshot" && matches(o, v) && owned(o, "VirtualMachine", v.Native.UID) && o.GetLabels()["vm.ani.io/snapshot-id"] == s.ID && (s.Native.UID == "" || string(o.GetUID()) == s.Native.UID) && snapshotString(o, "spec", "source", "kind") == "VirtualMachine" && snapshotString(o, "spec", "source", "apiGroup") == "kubevirt.io" && snapshotString(o, "spec", "source", "name") == v.Native.Name && snapshotString(o, "spec", "deletionPolicy") == "Delete"
}
func (n *Native) EnsureSnapshot(ctx context.Context, v biz.VM, s biz.Snapshot) (biz.Snapshot, error) {
	if _, err := n.snapshotRoot(ctx, v); err != nil {
		return s, err
	}
	if err := n.snapshotClassesUnchanged(ctx, s); err != nil {
		return s, err
	}
	old, err := n.client.Resource(snapshotResource).Namespace(v.Spec.Namespace).Get(ctx, s.Native.Name, metav1.GetOptions{})
	if err == nil {
		if !validSnapshotObject(old, v, s) {
			return s, biz.ErrOwnership
		}
		s.Native = objectRef(old)
		return n.ObserveSnapshot(ctx, v, s)
	}
	if !apierrors.IsNotFound(err) {
		return s, err
	}
	if s.Native.UID != "" {
		return s, biz.ErrOwnership
	}
	labels := labelVM(v)
	labels["vm.ani.io/snapshot-id"] = s.ID
	object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "snapshot.kubevirt.io/v1beta1", "kind": "VirtualMachineSnapshot", "metadata": map[string]any{"name": s.Native.Name, "namespace": v.Spec.Namespace}, "spec": map[string]any{"source": map[string]any{"apiGroup": "kubevirt.io", "kind": "VirtualMachine", "name": v.Native.Name}, "deletionPolicy": "Delete"}}}
	object.SetLabels(labels)
	object.SetOwnerReferences(ownReference(v))
	created, err := n.client.Resource(snapshotResource).Namespace(v.Spec.Namespace).Create(ctx, object, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return n.EnsureSnapshot(ctx, v, s)
	}
	if err != nil {
		return s, err
	}
	s.Native = objectRef(created)
	return n.ObserveSnapshot(ctx, v, s)
}
func (n *Native) ObserveSnapshot(ctx context.Context, v biz.VM, s biz.Snapshot) (biz.Snapshot, error) {
	if err := n.namespace(ctx, v); err != nil {
		return s, err
	}
	object, err := n.client.Resource(snapshotResource).Namespace(v.Spec.Namespace).Get(ctx, s.Native.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		s.Ready = false
		s.Complete = false
		s.Reason = "VM_SNAPSHOT_NATIVE_ABSENT"
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if !validSnapshotObject(object, v, s) {
		return s, biz.ErrOwnership
	}
	s.Native = objectRef(object)
	s.CreatedAt = object.GetCreationTimestamp().Time
	s.ObservedAt = time.Now().UTC()
	if uid := snapshotString(object, "status", "sourceUID"); uid != "" && uid != s.SourceVMUID {
		return s, biz.ErrOwnership
	}
	s.Ready = snapshotBool(object, "status", "readyToUse") && object.GetDeletionTimestamp() == nil
	s.Failed = snapshotString(object, "status", "phase") == "Failed"
	s.Included, _, _ = unstructured.NestedStringSlice(object.Object, "status", "snapshotVolumes", "includedVolumes")
	s.Excluded, _, _ = unstructured.NestedStringSlice(object.Object, "status", "snapshotVolumes", "excludedVolumes")
	s.Indications, _, _ = unstructured.NestedStringSlice(object.Object, "status", "indications")
	indications, _, _ := unstructured.NestedSlice(object.Object, "status", "sourceIndications")
	for _, value := range indications {
		if m, ok := value.(map[string]any); ok {
			if code, ok := m["indication"].(string); ok {
				found := false
				for _, old := range s.Indications {
					if old == code {
						found = true
					}
				}
				if !found {
					s.Indications = append(s.Indications, code)
				}
			}
		}
	}
	sort.Strings(s.Included)
	sort.Strings(s.Excluded)
	sort.Strings(s.Indications)
	contentName := snapshotString(object, "status", "virtualMachineSnapshotContentName")
	if contentName == "" {
		s.Ready = false
		s.Complete = false
		s.Reason = "VM_SNAPSHOT_PREPARING"
		return s, nil
	}
	content, err := n.client.Resource(snapshotContentResource).Namespace(v.Spec.Namespace).Get(ctx, contentName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		s.Ready = false
		s.Complete = false
		s.Reason = "VM_SNAPSHOT_CONTENT_ABSENT"
		return s, nil
	}
	if err != nil {
		return s, err
	}
	// Native content is protected by the snapshot controller/finalizers rather
	// than an OwnerReference to the Snapshot. Bind its deterministic first name,
	// source VM UID, snapshot reference and subsequently its immutable UID.
	if content.GetKind() != "VirtualMachineSnapshotContent" || content.GetAPIVersion() != "snapshot.kubevirt.io/v1beta1" || (s.Content.UID != "" && string(content.GetUID()) != s.Content.UID) || (s.Content.UID == "" && content.GetName() != "vmsnapshot-content-"+s.Native.UID) || snapshotString(content, "spec", "virtualMachineSnapshotName") != s.Native.Name || snapshotString(content, "spec", "source", "virtualMachine", "metadata", "uid") != s.SourceVMUID {
		return s, biz.ErrOwnership
	}
	s.Content = objectRef(content)
	s.Artifacts = snapshotRefs(s.Artifacts, s.Content)
	if !snapshotBool(content, "status", "readyToUse") {
		s.Ready = false
	}
	backups, _, _ := unstructured.NestedSlice(content.Object, "spec", "volumeBackups")
	for index, required := range s.Required {
		_, backup, err := namedSnapshotBackups(backups, required.Name)
		if err != nil {
			return s, err
		}
		if backup == nil {
			s.Ready = false
			continue
		}
		original, _, _ := unstructured.NestedString(backup, "persistentVolumeClaim", "metadata", "uid")
		if original != required.PVC.UID {
			return s, biz.ErrOwnership
		}
		name, _, _ := unstructured.NestedString(backup, "volumeSnapshotName")
		if name == "" {
			s.Ready = false
			continue
		}
		vs, err := n.client.Resource(volumeSnapshotResource).Namespace(v.Spec.Namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			s.Ready = false
			continue
		}
		if err != nil {
			return s, err
		}
		if !snapshotOwner(vs, "VirtualMachineSnapshotContent", content.GetName(), string(content.GetUID())) || snapshotString(vs, "spec", "volumeSnapshotClassName") != required.Class.Name || snapshotString(vs, "spec", "source", "persistentVolumeClaimName") != required.PVC.Name || !snapshotArtifactUnchanged(s, objectRef(vs)) {
			return s, biz.ErrOwnership
		}
		s.Required[index].VolumeSnapshot = objectRef(vs)
		s.Artifacts = snapshotRefs(s.Artifacts, objectRef(vs))
		if !snapshotBool(vs, "status", "readyToUse") {
			s.Ready = false
		}
		bound := snapshotString(vs, "status", "boundVolumeSnapshotContentName")
		if bound == "" {
			s.Ready = false
			continue
		}
		vsc, err := n.client.Resource(volumeSnapshotContentResource).Get(ctx, bound, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			s.Ready = false
			continue
		}
		if err != nil {
			return s, err
		}
		if snapshotString(vsc, "spec", "volumeSnapshotRef", "uid") != string(vs.GetUID()) || snapshotString(vsc, "spec", "volumeSnapshotRef", "namespace") != v.Spec.Namespace || snapshotString(vsc, "spec", "deletionPolicy") != "Delete" || !snapshotArtifactUnchanged(s, objectRef(vsc)) {
			return s, biz.ErrOwnership
		}
		s.Artifacts = snapshotRefs(s.Artifacts, objectRef(vsc))
	}
	s.Complete = biz.SnapshotComplete(s)
	s.Reason = "VM_SNAPSHOT_PREPARING"
	if s.Failed {
		s.Reason = "VM_SNAPSHOT_NATIVE_FAILED"
	} else if s.Ready {
		if s.Complete {
			s.Reason = ""
		} else {
			s.Reason = "VM_SNAPSHOT_INCOMPLETE"
		}
	}
	return s, nil
}
func namedSnapshotBackups(items []any, name string) (int, map[string]any, error) {
	var result map[string]any
	index := -1
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return -1, nil, biz.ErrChanged
		}
		if m["volumeName"] == name {
			if result != nil {
				return -1, nil, biz.ErrChanged
			}
			result = m
			index = i
		}
	}
	return index, result, nil
}
func snapshotArtifactUnchanged(s biz.Snapshot, ref biz.ObjectRef) bool {
	for _, old := range s.Artifacts {
		if old.Kind == ref.Kind && old.Namespace == ref.Namespace && old.Name == ref.Name {
			return old.UID == ref.UID
		}
	}
	return true
}
func snapshotResourceFor(kind string) (schema.GroupVersionResource, bool) {
	switch kind {
	case "VirtualMachineSnapshotContent":
		return snapshotContentResource, true
	case "VolumeSnapshot":
		return volumeSnapshotResource, true
	case "VolumeSnapshotContent":
		return volumeSnapshotContentResource, true
	case "VirtualMachineRestore":
		return restoreResource, true
	}
	return schema.GroupVersionResource{}, false
}
func (n *Native) DeleteSnapshot(ctx context.Context, v biz.VM, s biz.Snapshot) (bool, error) {
	if err := n.namespace(ctx, v); err != nil {
		return false, err
	}
	for _, ref := range s.Restores {
		object, err := n.client.Resource(restoreResource).Namespace(v.Spec.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !validRestoreObject(object, v, s, ref) {
			return false, biz.ErrOwnership
		}
		uid := object.GetUID()
		err = n.client.Resource(restoreResource).Namespace(v.Spec.Namespace).Delete(ctx, ref.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
		return false, err
	}
	object, err := n.client.Resource(snapshotResource).Namespace(v.Spec.Namespace).Get(ctx, s.Native.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	if err == nil {
		if !validSnapshotObject(object, v, s) {
			return false, biz.ErrOwnership
		}
		uid := object.GetUID()
		policy := metav1.DeletePropagationForeground
		err = n.client.Resource(snapshotResource).Namespace(v.Spec.Namespace).Delete(ctx, s.Native.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}, PropagationPolicy: &policy})
		return false, err
	}
	for _, ref := range s.Artifacts {
		resource, ok := snapshotResourceFor(ref.Kind)
		if !ok || ref.UID == "" {
			return false, biz.ErrOwnership
		}
		var child *unstructured.Unstructured
		if ref.Namespace != "" {
			child, err = n.client.Resource(resource).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		} else {
			child, err = n.client.Resource(resource).Get(ctx, ref.Name, metav1.GetOptions{})
		}
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		if string(child.GetUID()) != ref.UID {
			return false, biz.ErrOwnership
		}
		return false, nil
	}
	return true, nil
}

func validRestoreObject(o *unstructured.Unstructured, v biz.VM, s biz.Snapshot, ref biz.ObjectRef) bool {
	return o.GetKind() == "VirtualMachineRestore" && o.GetAPIVersion() == "snapshot.kubevirt.io/v1beta1" && matches(o, v) && owned(o, "VirtualMachine", v.Native.UID) && (ref.UID == "" || string(o.GetUID()) == ref.UID) && o.GetAnnotations()["vm.ani.io/snapshot-uid"] == s.Native.UID && o.GetAnnotations()["vm.ani.io/source-vm-uid"] == v.Native.UID && snapshotString(o, "spec", "target", "kind") == "VirtualMachine" && snapshotString(o, "spec", "target", "apiGroup") == "kubevirt.io" && snapshotString(o, "spec", "target", "name") == v.Native.Name && snapshotString(o, "spec", "virtualMachineSnapshotName") == s.Native.Name && snapshotString(o, "spec", "targetReadinessPolicy") == "FailImmediate" && snapshotString(o, "spec", "volumeRestorePolicy") == "RandomizeNames" && snapshotString(o, "spec", "volumeOwnershipPolicy") == "Vm"
}
func (n *Native) EnsureRestore(ctx context.Context, v biz.VM, s biz.Snapshot, ref biz.ObjectRef) (biz.RestoreObservation, error) {
	result := biz.RestoreObservation{Native: ref}
	root, err := n.snapshotRoot(ctx, v)
	if err != nil {
		return result, err
	}
	vmi, err := n.client.Resource(vmiResource).Namespace(v.Spec.Namespace).Get(ctx, v.Native.Name, metav1.GetOptions{})
	if err == nil && vmi != nil {
		return result, biz.ErrBusy
	}
	if !apierrors.IsNotFound(err) {
		return result, err
	}
	strategy := snapshotString(root, "spec", "runStrategy")
	running := snapshotBool(root, "spec", "running")
	if running || (strategy != "" && strategy != "Halted" && strategy != "Manual") {
		return result, biz.ErrBusy
	}
	fresh, err := n.ObserveSnapshot(ctx, v, s)
	if err != nil {
		return result, err
	}
	if !fresh.Complete || !biz.SnapshotComplete(fresh) || fresh.Native.UID != s.Native.UID || fresh.Content.UID != s.Content.UID {
		return result, biz.ErrChanged
	}
	object, err := n.client.Resource(restoreResource).Namespace(v.Spec.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if ref.UID != "" {
			return result, biz.ErrOwnership
		}
		labels := labelVM(v)
		labels["vm.ani.io/snapshot-id"] = s.ID
		object = &unstructured.Unstructured{Object: map[string]any{"apiVersion": "snapshot.kubevirt.io/v1beta1", "kind": "VirtualMachineRestore", "metadata": map[string]any{"name": ref.Name, "namespace": v.Spec.Namespace}, "spec": map[string]any{"target": map[string]any{"apiGroup": "kubevirt.io", "kind": "VirtualMachine", "name": v.Native.Name}, "virtualMachineSnapshotName": s.Native.Name, "targetReadinessPolicy": "FailImmediate", "volumeRestorePolicy": "RandomizeNames", "volumeOwnershipPolicy": "Vm"}}}
		object.SetLabels(labels)
		object.SetOwnerReferences(ownReference(v))
		object.SetAnnotations(map[string]string{"vm.ani.io/snapshot-uid": s.Native.UID, "vm.ani.io/source-vm-uid": v.Native.UID})
		created, err := n.client.Resource(restoreResource).Namespace(v.Spec.Namespace).Create(ctx, object, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			return n.EnsureRestore(ctx, v, s, ref)
		}
		if err != nil {
			return result, err
		}
		result.Native = objectRef(created)
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !validRestoreObject(object, v, s, ref) {
		return result, biz.ErrOwnership
	}
	return n.observeRestoreObject(ctx, v, s, ref, object, true)
}

func (n *Native) restoredPVCReferences(ctx context.Context, pvc *unstructured.Unstructured) ([]biz.ObjectRef, error) {
	name := snapshotString(pvc, "spec", "volumeName")
	if name == "" {
		return nil, nil
	}
	pv, err := n.client.Resource(pvResource).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if pv.GetAPIVersion() != "v1" || pv.GetKind() != "PersistentVolume" || pv.GetUID() == "" || snapshotString(pv, "spec", "claimRef", "namespace") != pvc.GetNamespace() || snapshotString(pv, "spec", "claimRef", "name") != pvc.GetName() || snapshotString(pv, "spec", "claimRef", "uid") != string(pvc.GetUID()) || snapshotString(pv, "spec", "persistentVolumeReclaimPolicy") != "Delete" {
		return nil, biz.ErrOwnership
	}
	return []biz.ObjectRef{objectRef(pv)}, nil
}
func (n *Native) ApplyRestoredConfiguration(ctx context.Context, v biz.VM, s biz.Snapshot) (bool, error) {
	root, err := n.snapshotRoot(ctx, v)
	if err != nil {
		return false, err
	}
	_, err = n.client.Resource(vmiResource).Namespace(v.Spec.Namespace).Get(ctx, v.Native.Name, metav1.GetOptions{})
	if err == nil {
		return false, biz.ErrBusy
	}
	if !apierrors.IsNotFound(err) {
		return false, err
	}
	volumes, _, _ := unstructured.NestedSlice(root.Object, "spec", "template", "spec", "volumes")
	devices, _, _ := unstructured.NestedSlice(root.Object, "spec", "template", "spec", "domain", "devices", "disks")
	newVolumes := plainSlice(volumes)
	newDevices := plainSlice(devices)
	for _, disk := range v.Disks {
		if disk.Tombstone || !disk.Attached {
			continue
		}
		pvc, err := n.client.Resource(pvcResource).Namespace(v.Spec.Namespace).Get(ctx, disk.PVC.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if disk.PVC.UID == "" || string(pvc.GetUID()) != disk.PVC.UID || !owned(pvc, "VirtualMachine", v.Native.UID) || snapshotString(pvc, "spec", "volumeMode") != "Block" {
			return false, biz.ErrOwnership
		}
		_, volume, err := namedItems(newVolumes, disk.VolumeName)
		if err != nil {
			return false, err
		}
		if volume == nil {
			return false, biz.ErrChanged
		}
		claim, _, _ := unstructured.NestedString(volume, "persistentVolumeClaim", "claimName")
		if claim != disk.PVC.Name || disk.DV.Name != "" {
			return false, biz.ErrOwnership
		}
		if disk.Role == "data" {
			if err = unstructured.SetNestedField(volume, true, "persistentVolumeClaim", "hotpluggable"); err != nil {
				return false, err
			}
			_, device, err := namedItems(newDevices, disk.VolumeName)
			if err != nil {
				return false, err
			}
			if device == nil {
				return false, biz.ErrChanged
			}
			device["disk"] = map[string]any{"bus": "scsi"}
			device["serial"] = disk.Serial
			delete(device, "lun")
			delete(device, "cdrom")
		}
	}
	for _, copy := range s.MediaCopies {
		current, ok := biz.FindMediaCopy(v, copy.ID)
		if !ok {
			return false, biz.ErrOwnership
		}
		pvc, err := n.client.Resource(pvcResource).Namespace(v.Spec.Namespace).Get(ctx, current.PVC.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if current.PVC.UID == "" || string(pvc.GetUID()) != current.PVC.UID || !owned(pvc, "VirtualMachine", v.Native.UID) {
			return false, biz.ErrOwnership
		}
	}
	domain, _, _ := unstructured.NestedMap(root.Object, "spec", "template", "spec", "domain")
	newVolumes, newDevices, err = n.MediaTemplate(v, domain, newVolumes, newDevices)
	if err != nil {
		return false, err
	}
	beforeVolumes, _ := json.Marshal(volumes)
	afterVolumes, _ := json.Marshal(newVolumes)
	beforeDevices, _ := json.Marshal(devices)
	afterDevices, _ := json.Marshal(newDevices)
	if string(beforeVolumes) == string(afterVolumes) && string(beforeDevices) == string(afterDevices) {
		return true, nil
	}
	patch := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": v.Native.UID}, {"op": "test", "path": "/metadata/resourceVersion", "value": root.GetResourceVersion()}, {"op": "add", "path": "/spec/template/spec/volumes", "value": newVolumes}, {"op": "add", "path": "/spec/template/spec/domain/devices/disks", "value": newDevices}}
	body, _ := json.Marshal(patch)
	_, err = n.client.Resource(vmResource).Namespace(v.Spec.Namespace).Patch(ctx, v.Native.Name, types.JSONPatchType, body, metav1.PatchOptions{})
	return false, err
}

var _ biz.SnapshotNativePort = (*Native)(nil)

// ObserveRetiredVolume does not resolve a catalog or require a current root VM.
// It freezes late-bound PVs by their claim UID before the existing delete port
// mutates anything, so a superseding DELETE can still prove their reclamation.
func (n *Native) ObserveRetiredVolume(ctx context.Context, v biz.VM, d biz.Disk) (biz.Disk, error) {
	if err := n.namespace(ctx, v); err != nil {
		return d, err
	}
	if d.PVC.Kind != "PersistentVolumeClaim" || d.PVC.Namespace != v.Spec.Namespace || d.PVC.Name == "" || d.PVC.UID == "" {
		return d, biz.ErrOwnership
	}
	pvc, err := n.client.Resource(pvcResource).Namespace(d.PVC.Namespace).Get(ctx, d.PVC.Name, metav1.GetOptions{})
	if err == nil {
		if objectRef(pvc) != d.PVC || (!owned(pvc, "VirtualMachine", v.Native.UID) && !owned(pvc, "DataVolume", d.DV.UID)) {
			return d, biz.ErrOwnership
		}
	} else if !apierrors.IsNotFound(err) {
		return d, err
	}
	refs := append([]biz.ObjectRef(nil), d.Cleanup...)
	remember := func(pv *unstructured.Unstructured) error {
		if pv.GetAPIVersion() != "v1" || pv.GetKind() != "PersistentVolume" || pv.GetUID() == "" || snapshotString(pv, "spec", "claimRef", "uid") != d.PVC.UID || snapshotString(pv, "spec", "claimRef", "namespace") != d.PVC.Namespace || snapshotString(pv, "spec", "claimRef", "name") != d.PVC.Name || snapshotString(pv, "spec", "persistentVolumeReclaimPolicy") != "Delete" {
			return biz.ErrOwnership
		}
		ref := objectRef(pv)
		for _, old := range refs {
			if old.Name == ref.Name {
				if old != ref {
					return biz.ErrOwnership
				}
				return nil
			}
		}
		refs = append(refs, ref)
		return nil
	}
	for _, ref := range d.Cleanup {
		if ref.Kind != "PersistentVolume" || ref.Namespace != "" || ref.Name == "" || ref.UID == "" {
			return d, biz.ErrOwnership
		}
		pv, err := n.client.Resource(pvResource).Get(ctx, ref.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return d, err
		}
		if objectRef(pv) != ref {
			return d, biz.ErrOwnership
		}
		if err = remember(pv); err != nil {
			return d, err
		}
	}
	list, err := n.client.Resource(pvResource).List(ctx, metav1.ListOptions{})
	if err != nil {
		return d, err
	}
	for i := range list.Items {
		pv := &list.Items[i]
		if snapshotString(pv, "spec", "claimRef", "uid") != d.PVC.UID {
			continue
		}
		if err = remember(pv); err != nil {
			return d, err
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	d.Cleanup = refs
	return d, nil
}

var _ biz.RetiredVolumeNativePort = (*Native)(nil)

// This observation path is safe during closing: it never calls EnsureRestore,
// creates a Restore or changes power. It also recovers PVCs from a lost status
// update using the native exact lastRestoreUID annotation and snapshot source.
func (n *Native) ObserveRestore(ctx context.Context, v biz.VM, s biz.Snapshot, ref biz.ObjectRef) (biz.RestoreObservation, error) {
	result := biz.RestoreObservation{Native: ref}
	if err := n.namespace(ctx, v); err != nil {
		return result, err
	}
	if ref.Kind != "VirtualMachineRestore" || ref.Namespace != v.Spec.Namespace || ref.Name == "" || s.SourceVMID != v.ID || s.SourceVMUID != v.Native.UID {
		return result, biz.ErrOwnership
	}
	object, err := n.client.Resource(restoreResource).Namespace(v.Spec.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return result, err
	}
	if apierrors.IsNotFound(err) {
		object = nil
	}
	return n.observeRestoreObject(ctx, v, s, ref, object, false)
}
func (n *Native) observeRestoreObject(ctx context.Context, v biz.VM, s biz.Snapshot, ref biz.ObjectRef, object *unstructured.Unstructured, requireCurrent bool) (biz.RestoreObservation, error) {
	result := biz.RestoreObservation{Native: ref, Absent: object == nil}
	if object != nil {
		if !validRestoreObject(object, v, s, ref) {
			return result, biz.ErrOwnership
		}
		result.Native = objectRef(object)
		result.Complete = snapshotBool(object, "status", "complete")
		conditions, _, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
		for _, value := range conditions {
			if condition, ok := value.(map[string]any); ok && condition["type"] == "Failure" && condition["status"] == "True" {
				result.Failed = true
				result.Reason = "VM_RESTORE_NATIVE_FAILED"
			}
		}
	}
	var root *unstructured.Unstructured
	var err error
	if requireCurrent {
		root, err = n.snapshotRoot(ctx, v)
		if err != nil {
			return result, err
		}
	} else {
		root, err = n.client.Resource(vmResource).Namespace(v.Spec.Namespace).Get(ctx, v.Native.Name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return result, err
		}
		if apierrors.IsNotFound(err) {
			root = nil
		}
		if root != nil && (root.GetAPIVersion() != "kubevirt.io/v1" || root.GetKind() != "VirtualMachine" || string(root.GetUID()) != v.Native.UID || !matches(root, v)) {
			return result, biz.ErrOwnership
		}
	}
	list, err := n.client.Resource(pvcResource).Namespace(v.Spec.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, err
	}
	if result.Native.UID == "" {
		// A native completed VM carries the immutable Restore identity even if
		// its response or the following platform checkpoint was lost.
		last := ""
		if root != nil {
			last = root.GetAnnotations()["restore.kubevirt.io/lastRestoreUID"]
		}
		prefix := ref.Name + "-"
		if strings.HasPrefix(last, prefix) {
			uid := strings.TrimPrefix(last, prefix)
			if !biz.UUID(uid) {
				return result, biz.ErrUnknown
			}
			result.Native.UID = uid
		}
	}
	if result.Native.UID == "" {
		// A partially created PVC may outlive a removed Restore before the VM's
		// completion annotation exists. Bind one unambiguous immutable UID from
		// native annotations; the owner and frozen snapshot source are checked below.
		prefix := ref.Name + "-"
		for i := range list.Items {
			marker := list.Items[i].GetAnnotations()["restore.kubevirt.io/lastRestoreUID"]
			if !strings.HasPrefix(marker, prefix) {
				continue
			}
			uid := strings.TrimPrefix(marker, prefix)
			if !biz.UUID(uid) {
				return result, biz.ErrUnknown
			}
			if result.Native.UID != "" && result.Native.UID != uid {
				return result, biz.ErrOwnership
			}
			result.Native.UID = uid
		}
	}
	if result.Native.UID == "" {
		if root == nil {
			return result, biz.ErrUnknown
		}
		return result, nil
	} // no existing object or native identity
	marker := result.Native.Name + "-" + result.Native.UID
	if requireCurrent && result.Complete && root.GetAnnotations()["restore.kubevirt.io/lastRestoreUID"] != marker {
		return result, biz.ErrOwnership
	}
	requiredByName := map[string]biz.SnapshotVolume{}
	requiredBySnapshot := map[string]biz.SnapshotVolume{}
	for _, required := range s.Required {
		requiredByName[required.Name] = required
		if required.VolumeSnapshot.Name != "" {
			if _, duplicate := requiredBySnapshot[required.VolumeSnapshot.Name]; duplicate {
				return result, biz.ErrChanged
			}
			requiredBySnapshot[required.VolumeSnapshot.Name] = required
		}
	}
	restoredByPVC := map[string]biz.SnapshotVolume{}
	if object != nil {
		restores, _, _ := unstructured.NestedSlice(object.Object, "status", "restores")
		for _, value := range restores {
			entry, ok := value.(map[string]any)
			if !ok {
				return result, biz.ErrChanged
			}
			volume, _, _ := unstructured.NestedString(entry, "volumeName")
			required, ok := requiredByName[volume]
			if !ok {
				return result, biz.ErrOwnership
			}
			name, _, _ := unstructured.NestedString(entry, "persistentVolumeClaim")
			source, _, _ := unstructured.NestedString(entry, "volumeSnapshotName")
			if name == "" || source == "" || (required.VolumeSnapshot.Name != "" && required.VolumeSnapshot.Name != source) {
				return result, biz.ErrOwnership
			}
			if _, duplicate := restoredByPVC[name]; duplicate {
				return result, biz.ErrChanged
			}
			required.VolumeSnapshot.Name = source
			restoredByPVC[name] = required
		}
	}
	var volumes []any
	if root != nil {
		volumes, _, _ = unstructured.NestedSlice(root.Object, "spec", "template", "spec", "volumes")
	}
	seen := map[string]bool{}
	for i := range list.Items {
		pvc := &list.Items[i]
		if pvc.GetAnnotations()["restore.kubevirt.io/lastRestoreUID"] != marker {
			continue
		}
		if pvc.GetUID() == "" || !owned(pvc, "VirtualMachine", v.Native.UID) || snapshotString(pvc, "spec", "dataSource", "apiGroup") != "snapshot.storage.k8s.io" || snapshotString(pvc, "spec", "dataSource", "kind") != "VolumeSnapshot" {
			return result, biz.ErrOwnership
		}
		source := snapshotString(pvc, "spec", "dataSource", "name")
		required, ok := restoredByPVC[pvc.GetName()]
		if !ok {
			required, ok = requiredBySnapshot[source]
		}
		if !ok {
			return result, biz.ErrUnknown
		}
		if required.VolumeSnapshot.Name != source || seen[required.Name] {
			return result, biz.ErrOwnership
		}
		seen[required.Name] = true
		if required.VolumeSnapshot.UID != "" {
			vs, err := n.client.Resource(volumeSnapshotResource).Namespace(v.Spec.Namespace).Get(ctx, source, metav1.GetOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				return result, err
			}
			if err == nil && objectRef(vs) != required.VolumeSnapshot {
				return result, biz.ErrOwnership
			}
		}

		mode := snapshotString(pvc, "spec", "volumeMode")
		if mode == "" {
			mode = "Filesystem"
		}
		if required.Storage.Name != "" && snapshotString(pvc, "spec", "storageClassName") != required.Storage.Name {
			return result, biz.ErrOwnership
		}
		if required.Role == "backend" {
			if mode != "Filesystem" {
				return result, biz.ErrOwnership
			}
			state, err := n.observeBackendPVC(ctx, v, pvc, biz.BackendState{})
			if err != nil {
				return result, err
			}
			state.Current = requireCurrent && result.Complete
			result.BackendStates = append(result.BackendStates, state)
			continue
		}
		if (required.Role == "system" || required.Role == "data") && mode != "Block" {
			return result, biz.ErrOwnership
		}
		if requireCurrent && result.Complete {
			_, volume, err := namedItems(volumes, required.Name)
			if err != nil {
				return result, err
			}
			claim, _, _ := unstructured.NestedString(volume, "persistentVolumeClaim", "claimName")
			if claim != pvc.GetName() {
				return result, biz.ErrOwnership
			}
		}
		cleanup, err := n.restoredPVCReferences(ctx, pvc)
		if err != nil {
			return result, err
		}
		if required.Role == "media" {
			found := false
			for _, copy := range s.MediaCopies {
				if copy.ID == required.MediaCopyID {
					if copy.Source.VolumeMode != mode {
						return result, biz.ErrOwnership
					}
					copy.DV = biz.ObjectRef{}
					copy.PVC = objectRef(pvc)
					copy.Cleanup = cleanup
					copy.Ready = snapshotString(pvc, "status", "phase") == "Bound"
					copy.Tombstone = false
					result.MediaCopies = append(result.MediaCopies, copy)
					found = true
					break
				}
			}
			if !found {
				return result, biz.ErrOwnership
			}
		} else {
			found := false
			for _, disk := range s.Inventory {
				if disk.ID == required.DiskID {
					disk.DV = biz.ObjectRef{}
					disk.PVC = objectRef(pvc)
					disk.Cleanup = cleanup
					disk.Preparation = snapshotString(pvc, "status", "phase")
					disk.Reason = ""
					disk.Attached = true
					disk.Tombstone = false
					result.Disks = append(result.Disks, disk)
					found = true
					break
				}
			}
			if !found {
				return result, biz.ErrOwnership
			}
		}
	}
	if requireCurrent && result.Complete {
		for _, required := range s.Required {
			if !seen[required.Name] {
				return result, biz.ErrDependency
			}
		}
	}
	return result, nil
}

var _ biz.RestoreObserver = (*Native)(nil)
