package instance

import (
	"context"
	"fmt"
	"sort"
	"time"

	biz "github.com/zhangzhe-ctrl/ani-resource-service/internal/biz/instance"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var eventGVR = schema.GroupVersionResource{Version: "v1", Resource: "events"}

type runtimeTarget struct{ notebook, statefulSet, pod *unstructured.Unstructured }

// resolveRuntimeTarget is a GET/LIST-only owner-chain check. It deliberately
// does not call Observe: Observe also signs GPU Pods and observes cleanup.
func (n *Notebook) resolveRuntimeTarget(ctx context.Context, i biz.Instance) (runtimeTarget, error) {
	var target runtimeTarget
	notebook, e := n.client.Resource(notebookGVR).Namespace(i.Spec.Namespace).Get(ctx, i.Spec.NotebookName, metav1.GetOptions{})
	if e == nil {
		if !owned(notebook, i, i.NotebookUID) || notebook.GetUID() == "" {
			return target, biz.ErrOwnership
		}
		target.notebook = notebook
	} else if !apierrors.IsNotFound(e) {
		return target, e
	}
	set, e := n.client.Resource(stsGVR).Namespace(i.Spec.Namespace).Get(ctx, i.Spec.NotebookName, metav1.GetOptions{})
	if e == nil {
		if target.notebook == nil || set.GetUID() == "" || set.GetNamespace() != i.Spec.Namespace || !controlledBy(set, "kubeflow.org", "Notebook", i.Spec.NotebookName, string(target.notebook.GetUID())) {
			return target, biz.ErrOwnership
		}
		target.statefulSet = set
	} else if !apierrors.IsNotFound(e) {
		return target, e
	}
	pods, e := n.client.Resource(podGVR).Namespace(i.Spec.Namespace).List(ctx, metav1.ListOptions{LabelSelector: resourceLabel + "=" + i.ID, Limit: 2})
	if e != nil {
		return target, e
	}
	if len(pods.Items) > 1 || pods.GetContinue() != "" {
		return target, biz.ErrOwnership
	}
	if len(pods.Items) == 1 {
		p := &pods.Items[0]
		if target.statefulSet == nil || p.GetUID() == "" || !owned(p, i, "") || !controlledBy(p, "apps", "StatefulSet", i.Spec.NotebookName, string(target.statefulSet.GetUID())) {
			return target, biz.ErrOwnership
		}
		if e = verifyPod(p, i); e != nil {
			return target, fmt.Errorf("%w: managed workspace differs", biz.ErrOwnership)
		}
		if e = n.verifyPodGroupPriority(ctx, p, i); e != nil {
			return target, e
		}
		target.pod = p
	}
	return target, nil
}
func nestedString(o map[string]any, path ...string) string {
	v, _, _ := unstructured.NestedString(o, path...)
	return v
}
func (n *Notebook) ReadRuntime(ctx context.Context, i biz.Instance) (biz.Runtime, error) {
	target, e := n.resolveRuntimeTarget(ctx, i)
	if e != nil {
		return biz.Runtime{}, e
	}
	out := biz.Runtime{ObservedAt: time.Now().UTC()}
	if set := target.statefulSet; set != nil {
		out.StatefulSet = &biz.RuntimeStatefulSet{}
		if v, ok, _ := unstructured.NestedInt64(set.Object, "spec", "replicas"); ok {
			v32 := int32(v)
			out.StatefulSet.DesiredReplicas = &v32
		}
		if v, ok, _ := unstructured.NestedInt64(set.Object, "status", "readyReplicas"); ok {
			v32 := int32(v)
			out.StatefulSet.ReadyReplicas = &v32
		}
	}
	p := target.pod
	if p == nil {
		return out, nil
	}
	pod := &biz.RuntimePod{Name: p.GetName(), UID: string(p.GetUID()), NodeName: nestedString(p.Object, "spec", "nodeName"), PodIP: nestedString(p.Object, "status", "podIP"), Phase: nestedString(p.Object, "status", "phase"), Reason: nestedString(p.Object, "status", "reason"), Message: nestedString(p.Object, "status", "message"), EnvironmentSources: []biz.EnvironmentSource{}, Mounts: []biz.RuntimeMount{}}
	conditions, _, _ := unstructured.NestedSlice(p.Object, "status", "conditions")
	ready := false
	for _, value := range conditions {
		c, _ := value.(map[string]any)
		if c["type"] == "Ready" && c["status"] == "True" {
			ready = true
		}
		if pod.Reason == "" && c["status"] == "False" {
			pod.Reason = nestedString(c, "reason")
			pod.Message = nestedString(c, "message")
		}
	}
	statuses, _, _ := unstructured.NestedSlice(p.Object, "status", "containerStatuses")
	for _, value := range statuses {
		c, _ := value.(map[string]any)
		if c["name"] != biz.Container {
			continue
		}
		r, _ := c["ready"].(bool)
		pod.Ready = pod.Phase == "Running" && ready && r
		for _, state := range []string{"waiting", "terminated"} {
			if v, ok, _ := unstructured.NestedMap(c, "state", state); ok {
				pod.Reason = nestedString(v, "reason")
				pod.Message = nestedString(v, "message")
			}
		}
	}
	volumes, _, _ := unstructured.NestedSlice(p.Object, "spec", "volumes")
	volumeTypes := map[string]string{}
	for _, value := range volumes {
		v, _ := value.(map[string]any)
		name, _ := v["name"].(string)
		kind := "unknown"
		for _, candidate := range []string{"persistentVolumeClaim", "emptyDir", "configMap", "secret", "projected", "downwardAPI", "hostPath", "csi", "ephemeral", "nfs"} {
			if _, ok := v[candidate]; ok {
				kind = candidate
				break
			}
		}
		volumeTypes[name] = kind
	}
	containers, _, _ := unstructured.NestedSlice(p.Object, "spec", "containers")
	for _, value := range containers {
		c, _ := value.(map[string]any)
		if c["name"] != biz.Container {
			continue
		}
		env, _, _ := unstructured.NestedSlice(c, "env")
		for _, value := range env {
			v, _ := value.(map[string]any)
			name := nestedString(v, "name")
			if name == "" {
				continue
			}
			kind := "literal"
			if from, ok, _ := unstructured.NestedMap(v, "valueFrom"); ok {
				kind = ""
				for field, category := range map[string]string{"configMapKeyRef": "configMap", "secretKeyRef": "secret", "fieldRef": "downwardAPI", "resourceFieldRef": "downwardAPI"} {
					if _, ok := from[field]; ok {
						kind = category
						break
					}
				}
			}
			if kind != "" {
				pod.EnvironmentSources = append(pod.EnvironmentSources, biz.EnvironmentSource{Name: name, SourceType: kind})
			}
		}
		mounts, _, _ := unstructured.NestedSlice(c, "volumeMounts")
		for _, value := range mounts {
			v, _ := value.(map[string]any)
			name := nestedString(v, "name")
			ro, _, _ := unstructured.NestedBool(v, "readOnly")
			pod.Mounts = append(pod.Mounts, biz.RuntimeMount{Name: name, VolumeType: volumeTypes[name], MountPath: nestedString(v, "mountPath"), ReadOnly: ro})
		}
	}
	out.Pod = pod
	return out, nil
}
func eventTime(e map[string]any) *time.Time {
	for _, path := range [][]string{{"series", "lastObservedTime"}, {"lastTimestamp"}, {"eventTime"}, {"firstTimestamp"}} {
		if raw := nestedString(e, path...); raw != "" {
			if v, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				v = v.UTC()
				return &v
			}
		}
	}
	return nil
}
func (n *Notebook) ReadEvents(ctx context.Context, i biz.Instance) (biz.Events, error) {
	target, e := n.resolveRuntimeTarget(ctx, i)
	if e != nil {
		return biz.Events{}, e
	}
	out := biz.Events{Items: []biz.PodEvent{}}
	if target.pod == nil {
		out.ObservedAt = time.Now().UTC()
		return out, nil
	}
	uid := string(target.pod.GetUID())
	out.PodUID = &uid
	continuation := ""
	read := 0
	for {
		limit := int64(200)
		if remaining := 1000 - read; remaining < int(limit) {
			limit = int64(remaining)
		}
		list, e := n.client.Resource(eventGVR).Namespace(i.Spec.Namespace).List(ctx, metav1.ListOptions{FieldSelector: "involvedObject.kind=Pod,involvedObject.uid=" + uid, Limit: limit, Continue: continuation})
		if e != nil {
			return biz.Events{}, e
		}
		for _, event := range list.Items {
			if read >= 1000 {
				out.Truncated = true
				break
			}
			read++
			if event.GetNamespace() != i.Spec.Namespace || nestedString(event.Object, "involvedObject", "kind") != "Pod" || nestedString(event.Object, "involvedObject", "uid") != uid {
				continue
			}
			v := biz.PodEvent{UID: string(event.GetUID()), Type: nestedString(event.Object, "type"), Reason: nestedString(event.Object, "reason"), Message: nestedString(event.Object, "message"), LastOccurredAt: eventTime(event.Object)}
			if count, ok, _ := unstructured.NestedInt64(event.Object, "series", "count"); ok {
				v.Count = &count
			} else if count, ok, _ := unstructured.NestedInt64(event.Object, "count"); ok {
				v.Count = &count
			}
			out.Items = append(out.Items, v)
		}
		continuation = list.GetContinue()
		if read >= 1000 {
			out.Truncated = out.Truncated || continuation != ""
			break
		}
		if continuation == "" {
			break
		}
	}
	sort.SliceStable(out.Items, func(a, b int) bool {
		x, y := out.Items[a], out.Items[b]
		if x.LastOccurredAt == nil && y.LastOccurredAt != nil {
			return false
		}
		if x.LastOccurredAt != nil && y.LastOccurredAt == nil {
			return true
		}
		if x.LastOccurredAt != nil && !x.LastOccurredAt.Equal(*y.LastOccurredAt) {
			return x.LastOccurredAt.After(*y.LastOccurredAt)
		}
		return x.UID < y.UID
	})
	out.ObservedAt = time.Now().UTC()
	return out, nil
}
