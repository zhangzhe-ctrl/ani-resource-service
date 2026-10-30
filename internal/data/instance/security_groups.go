package instance

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// patchSecurityGroupAnnotation is an absolute replacement protected by the exact
// UID and resource version. An interrupted request can safely be observed/retried.
func (n *Notebook) patchSecurityGroupAnnotation(ctx context.Context, resource schema.GroupVersionResource, obj *unstructured.Unstructured, value string, intentVersion int64) error {
	if obj.GetUID() == "" || obj.GetResourceVersion() == "" || obj.GetDeletionTimestamp() != nil {
		return biz.ErrVersion
	}
	ann := obj.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	version := strconv.FormatInt(intentVersion, 10)
	if current, present := ann[biz.SecurityGroupsAnnotation]; present && current == value && ann[versionAnnotation] == version {
		return nil
	}
	ann[biz.SecurityGroupsAnnotation] = value
	ann[versionAnnotation] = version
	raw, err := json.Marshal([]map[string]any{{"op": "test", "path": "/metadata/uid", "value": string(obj.GetUID())}, {"op": "test", "path": "/metadata/resourceVersion", "value": obj.GetResourceVersion()}, {"op": "add", "path": "/metadata/annotations", "value": ann}})
	if err != nil {
		return err
	}
	_, err = n.client.Resource(resource).Namespace(obj.GetNamespace()).Patch(ctx, obj.GetName(), types.JSONPatchType, raw, metav1.PatchOptions{})
	return err
}

func (n *Notebook) SetSecurityGroups(ctx context.Context, i biz.Instance, names []string) error {
	before, err := n.ObserveIdentity(ctx, i)
	if err != nil {
		return err
	}
	if !before.NotebookExists || before.NotebookUID == "" {
		return biz.ErrOwnership
	}
	value := strings.Join(names, ",") // Empty string explicitly detaches every group.
	root, err := n.lifecycleGet(ctx, notebookGVR, i.Spec.Namespace, i.Spec.NotebookName)
	if err != nil {
		return err
	}
	if !owned(root, i, before.NotebookUID) {
		return biz.ErrOwnership
	}
	if err = n.patchSecurityGroupAnnotation(ctx, notebookGVR, root, value, i.Version); err != nil {
		return err
	}
	for _, ref := range before.Pods {
		pod, err := n.lifecycleGet(ctx, podGVR, ref.Namespace, ref.Name)
		if err != nil {
			return err
		}
		if string(pod.GetUID()) != ref.UID || !controlledBy(pod, "apps", "StatefulSet", i.Spec.NotebookName, before.StatefulSetUID) || !owned(pod, i, ref.UID) {
			return biz.ErrOwnership
		}
		if err = n.patchSecurityGroupAnnotation(ctx, podGVR, pod, value, i.Version); err != nil {
			return err
		}
	}
	// The Notebook controller owns its StatefulSet. Its actual Pod template must
	// converge before old group references may be released.
	set, err := n.lifecycleGet(ctx, stsGVR, i.Spec.Namespace, i.Spec.NotebookName)
	if err == nil {
		if !controlledBy(set, "kubeflow.org", "Notebook", i.Spec.NotebookName, before.NotebookUID) || (before.StatefulSetUID != "" && string(set.GetUID()) != before.StatefulSetUID) {
			return biz.ErrOwnership
		}
		ann, _, err := unstructured.NestedStringMap(set.Object, "spec", "template", "metadata", "annotations")
		if err != nil {
			return err
		}
		if current, present := ann[biz.SecurityGroupsAnnotation]; !present || current != value {
			return biz.ErrVersion
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	after, err := n.ObserveIdentity(ctx, i)
	if err != nil {
		return err
	}
	if !after.NotebookExists || before.NotebookUID != after.NotebookUID || before.StatefulSetUID != after.StatefulSetUID || len(before.Pods) != len(after.Pods) {
		return biz.ErrVersion
	}
	root, err = n.lifecycleGet(ctx, notebookGVR, i.Spec.Namespace, i.Spec.NotebookName)
	if err != nil {
		return err
	}
	if !owned(root, i, before.NotebookUID) || root.GetAnnotations()[biz.SecurityGroupsAnnotation] != value || root.GetAnnotations()[versionAnnotation] != strconv.FormatInt(i.Version, 10) {
		return biz.ErrVersion
	}
	if before.StatefulSetUID != "" {
		latest, err := n.lifecycleGet(ctx, stsGVR, i.Spec.Namespace, i.Spec.NotebookName)
		if err != nil {
			return err
		}
		ann, _, err := unstructured.NestedStringMap(latest.Object, "spec", "template", "metadata", "annotations")
		if err != nil {
			return err
		}
		if string(latest.GetUID()) != before.StatefulSetUID || !controlledBy(latest, "kubeflow.org", "Notebook", i.Spec.NotebookName, before.NotebookUID) {
			return biz.ErrOwnership
		}
		if current, present := ann[biz.SecurityGroupsAnnotation]; !present || current != value {
			return biz.ErrVersion
		}
	}
	for _, ref := range after.Pods {
		known := false
		for _, old := range before.Pods {
			known = known || old == ref
		}
		if !known {
			return biz.ErrVersion
		}
		pod, err := n.lifecycleGet(ctx, podGVR, ref.Namespace, ref.Name)
		if err != nil {
			return err
		}
		if string(pod.GetUID()) != ref.UID || pod.GetAnnotations()[biz.SecurityGroupsAnnotation] != value || pod.GetAnnotations()[versionAnnotation] != strconv.FormatInt(i.Version, 10) {
			return biz.ErrVersion
		}
	}
	return nil
}
