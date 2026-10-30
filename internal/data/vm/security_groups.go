package vm

import (
	"context"
	"encoding/json"
	"strings"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/vm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func (n *Native) patchSecurityGroups(ctx context.Context, resource schema.GroupVersionResource, obj *unstructured.Unstructured, template bool, value string) error {
	if obj.GetUID() == "" || obj.GetResourceVersion() == "" || obj.GetDeletionTimestamp() != nil {
		return biz.ErrChanged
	}
	ann := obj.GetAnnotations()
	path := "/metadata/annotations"
	if template {
		var err error
		ann, _, err = unstructured.NestedStringMap(obj.Object, "spec", "template", "metadata", "annotations")
		if err != nil {
			return err
		}
		path = "/spec/template/metadata/annotations"
	}
	if ann == nil {
		ann = map[string]string{}
	}
	if current, present := ann[biz.SecurityGroupsAnnotation]; present && current == value {
		return nil
	}
	ann[biz.SecurityGroupsAnnotation] = value
	raw, err := json.Marshal([]map[string]any{{"op": "test", "path": "/metadata/uid", "value": string(obj.GetUID())}, {"op": "test", "path": "/metadata/resourceVersion", "value": obj.GetResourceVersion()}, {"op": "add", "path": path, "value": ann}})
	if err != nil {
		return err
	}
	_, err = n.client.Resource(resource).Namespace(obj.GetNamespace()).Patch(ctx, obj.GetName(), types.JSONPatchType, raw, metav1.PatchOptions{})
	return err
}

// SetSecurityGroups changes the future VM template, the active VMI metadata,
// and its launcher annotation using their individually verified exact UIDs.
func (n *Native) SetSecurityGroups(ctx context.Context, v biz.VM, names []string) error {
	if v.Native.UID == "" {
		return biz.ErrDependency
	}
	if err := n.namespace(ctx, v); err != nil {
		return err
	}
	before, err := n.observe(ctx, v, false)
	if err != nil {
		return err
	}
	if before.VM.UID != v.Native.UID {
		return biz.ErrOwnership
	}
	if len(before.StateChangeRequests) > 0 {
		return biz.ErrDependency
	}
	value := strings.Join(names, ",")
	root, err := n.client.Resource(vmResource).Namespace(v.Spec.Namespace).Get(ctx, v.Native.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if !matches(root, v) || string(root.GetUID()) != v.Native.UID {
		return biz.ErrOwnership
	}
	if err = n.patchSecurityGroups(ctx, vmResource, root, true, value); err != nil {
		return err
	}
	if before.VMI != nil {
		vmi, err := n.client.Resource(vmiResource).Namespace(v.Spec.Namespace).Get(ctx, before.VMI.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if string(vmi.GetUID()) != before.VMI.UID || !owned(vmi, "VirtualMachine", v.Native.UID) {
			return biz.ErrOwnership
		}
		if err = n.patchSecurityGroups(ctx, vmiResource, vmi, false, value); err != nil {
			return err
		}
	}
	if before.Launcher != nil {
		if before.VMI == nil {
			return biz.ErrDependency
		}
		pod, err := n.client.Resource(podResource).Namespace(v.Spec.Namespace).Get(ctx, before.Launcher.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if string(pod.GetUID()) != before.Launcher.UID || !owned(pod, "VirtualMachineInstance", before.VMI.UID) {
			return biz.ErrOwnership
		}
		if err = n.patchSecurityGroups(ctx, podResource, pod, false, value); err != nil {
			return err
		}
	}
	after, err := n.observe(ctx, v, false)
	if err != nil {
		return err
	}
	if after.VM != before.VM || !sameSecurityGroupRef(before.VMI, after.VMI) || !sameSecurityGroupRef(before.Launcher, after.Launcher) || len(after.StateChangeRequests) > 0 {
		return biz.ErrDependency
	}
	root, err = n.client.Resource(vmResource).Namespace(v.Spec.Namespace).Get(ctx, v.Native.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	ann, _, err := unstructured.NestedStringMap(root.Object, "spec", "template", "metadata", "annotations")
	if err != nil {
		return err
	}
	if !matches(root, v) || string(root.GetUID()) != v.Native.UID || ann[biz.SecurityGroupsAnnotation] != value {
		return biz.ErrDependency
	}
	for _, item := range []struct {
		ref      *biz.ObjectRef
		resource schema.GroupVersionResource
	}{{after.VMI, vmiResource}, {after.Launcher, podResource}} {
		if item.ref == nil {
			continue
		}
		obj, err := n.client.Resource(item.resource).Namespace(v.Spec.Namespace).Get(ctx, item.ref.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		ownerKind, ownerUID := "VirtualMachine", v.Native.UID
		if item.resource == podResource {
			ownerKind, ownerUID = "VirtualMachineInstance", after.VMI.UID
		}
		if string(obj.GetUID()) != item.ref.UID || !owned(obj, ownerKind, ownerUID) || obj.GetAnnotations()[biz.SecurityGroupsAnnotation] != value {
			return biz.ErrDependency
		}
	}
	return nil
}
func sameSecurityGroupRef(a, b *biz.ObjectRef) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
